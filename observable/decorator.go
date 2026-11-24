// Package observable provides a generic observable decorator pattern.
// It wraps function calls with OTel tracing spans and counter/histogram metrics.
//
// Design: the attrs you pass go to the SPAN in full (high cardinality is fine
// there). Only attribute keys whitelisted via WithMetricLabels become METRIC
// labels; every metric also gets an automatic status="ok"|"error" label. This
// keeps span detail rich while keeping metric series cardinality bounded — put
// IDs/accounts on the span, not in metric labels.
//
// Metrics are recorded with the call context, so exemplars (metric → trace
// links) attach automatically when the MeterProvider has exemplars enabled.
package observable

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

// Op represents a single observable operation with counter + latency histogram.
type Op struct {
	tracer       trace.Tracer
	counter      metric.Int64Counter
	latency      metric.Float64Histogram
	metricLabels map[string]struct{} // whitelist of attr keys allowed as metric labels
}

// NewOp creates an observable operation.
// name is used as the metric prefix: {name}_total (counter) and {name}_seconds (histogram).
// Instrument creation errors (e.g. an invalid name) are reported via the
// global otel error handler and the affected instrument degrades to a no-op.
func NewOp(meter metric.Meter, tracer trace.Tracer, name string, opts ...OpOption) *Op {
	cfg := opConfig{
		buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}
	for _, o := range opts {
		o(&cfg)
	}

	counter, err := meter.Int64Counter(name+"_total",
		metric.WithDescription("Total "+name+" operations"))
	if err != nil {
		otel.Handle(err)
		if counter == nil {
			counter, _ = noop.Meter{}.Int64Counter(name + "_total")
		}
	}
	latency, err := meter.Float64Histogram(name+"_seconds",
		metric.WithDescription(name+" operation latency"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(cfg.buckets...))
	if err != nil {
		otel.Handle(err)
		if latency == nil {
			latency, _ = noop.Meter{}.Float64Histogram(name + "_seconds")
		}
	}

	var labels map[string]struct{}
	if len(cfg.metricLabels) > 0 {
		labels = make(map[string]struct{}, len(cfg.metricLabels))
		for _, k := range cfg.metricLabels {
			labels[k] = struct{}{}
		}
	}

	return &Op{tracer: tracer, counter: counter, latency: latency, metricLabels: labels}
}

// Observe wraps a function call with a tracing span and metrics recording.
// attrs are set on the span; only whitelisted keys (WithMetricLabels) reach metrics.
// Panics are recorded as failures and rethrown with their original value.
func (o *Op) Observe(ctx context.Context, spanName string, attrs []attribute.KeyValue, fn func(ctx context.Context) error) error {
	ctx, span := o.tracer.Start(ctx, spanName, trace.WithAttributes(attrs...))
	start := time.Now()
	defer o.endCall(ctx, span, start, attrs)
	err := fn(ctx)
	o.finish(ctx, span, start, attrs, err)
	return err
}

// ObserveVal wraps a function that returns a value, with tracing and metrics.
// Panics are recorded as failures and rethrown with their original value.
func ObserveVal[T any](o *Op, ctx context.Context, spanName string, attrs []attribute.KeyValue, fn func(ctx context.Context) (T, error)) (T, error) {
	ctx, span := o.tracer.Start(ctx, spanName, trace.WithAttributes(attrs...))
	start := time.Now()
	defer o.endCall(ctx, span, start, attrs)

	result, err := fn(ctx)
	o.finish(ctx, span, start, attrs, err)
	return result, err
}

// endCall must be deferred directly so recover observes the active panic.
func (o *Op) endCall(ctx context.Context, span trace.Span, start time.Time, attrs []attribute.KeyValue) {
	if value := recover(); value != nil {
		o.finish(ctx, span, start, attrs, fmt.Errorf("panic: %v", value))
		// End before rethrowing: the SDK would otherwise record a second
		// exception if End were itself deferred during the panic.
		span.End()
		panic(value)
	}
	span.End()
}

// finish records the span status and the RED metrics. The span is marked failed
// (RecordError + SetStatus) on error; metrics get the whitelisted labels plus an
// automatic status, so error rate is derivable and cardinality stays bounded.
func (o *Op) finish(ctx context.Context, span trace.Span, start time.Time, attrs []attribute.KeyValue, err error) {
	status := "ok"
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
	}

	set := attribute.NewSet(o.metricAttrs(attrs, status)...)
	o.counter.Add(ctx, 1, metric.WithAttributeSet(set))
	o.latency.Record(ctx, time.Since(start).Seconds(), metric.WithAttributeSet(set))
}

// metricAttrs keeps only whitelisted attribute keys and appends status.
func (o *Op) metricAttrs(attrs []attribute.KeyValue, status string) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(o.metricLabels)+1)
	for _, a := range attrs {
		if _, ok := o.metricLabels[string(a.Key)]; ok {
			out = append(out, a)
		}
	}
	out = append(out, attribute.String("status", status))
	return out
}
