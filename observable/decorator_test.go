package observable

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupTest() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader, *tracetest.SpanRecorder) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	_ = tp

	return mp, reader, spanRecorder
}

func TestOp_Observe_Success(t *testing.T) {
	mp, reader, _ := setupTest()
	meter := mp.Meter("test")

	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	tracer := tp.Tracer("test")

	op := NewOp(meter, tracer, "crypto.sign")

	err := op.Observe(context.Background(), "Sign", []attribute.KeyValue{
		attribute.String("key.id", "k1"),
	}, func(ctx context.Context) error {
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify span was created
	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name() != "Sign" {
		t.Errorf("span name = %q, want 'Sign'", spans[0].Name())
	}

	// Verify metrics were recorded
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	metricNames := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metricNames[m.Name] = true
		}
	}
	if !metricNames["crypto.sign_total"] {
		t.Error("missing crypto.sign_total metric")
	}
	if !metricNames["crypto.sign_seconds"] {
		t.Error("missing crypto.sign_seconds metric")
	}
}

func TestOp_Observe_Error(t *testing.T) {
	mp, _, _ := setupTest()
	meter := mp.Meter("test")

	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	tracer := tp.Tracer("test")

	op := NewOp(meter, tracer, "crypto.verify")

	testErr := errors.New("verification failed")
	err := op.Observe(context.Background(), "Verify", nil, func(ctx context.Context) error {
		return testErr
	})

	if !errors.Is(err, testErr) {
		t.Fatalf("expected testErr, got %v", err)
	}

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	// Span should have recorded the error event AND been marked failed.
	events := spans[0].Events()
	hasError := false
	for _, e := range events {
		if e.Name == "exception" {
			hasError = true
		}
	}
	if !hasError {
		t.Error("span should have recorded error event")
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status().Code)
	}
}

func TestObserveVal(t *testing.T) {
	mp, _, _ := setupTest()
	meter := mp.Meter("test")

	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	tracer := tp.Tracer("test")

	op := NewOp(meter, tracer, "crypto.keygen")

	result, err := ObserveVal(op, context.Background(), "GenerateKey", nil, func(ctx context.Context) (string, error) {
		return "key-123", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "key-123" {
		t.Fatalf("expected 'key-123', got %q", result)
	}

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
}

// High-cardinality attrs must NOT leak into metric labels by default; the only
// metric label is the automatic status.
func TestOp_MetricLabels_DefaultDropsHighCardinality(t *testing.T) {
	mp, reader, _ := setupTest()
	tp := sdktrace.NewTracerProvider()
	op := NewOp(mp.Meter("test"), tp.Tracer("test"), "biz.op")

	_ = op.Observe(context.Background(), "Op", []attribute.KeyValue{
		attribute.String("key.id", "k1"), // high-card → span only
	}, func(ctx context.Context) error { return nil })

	dp := singleCounterDP(t, reader, "biz.op_total")
	if _, ok := dp.Attributes.Value("key.id"); ok {
		t.Error("key.id leaked into metric labels")
	}
	if v, ok := dp.Attributes.Value("status"); !ok || v.AsString() != "ok" {
		t.Errorf("status label = %v (present=%v), want ok", v.AsString(), ok)
	}
}

// Whitelisted keys become metric labels; others stay span-only. Error sets status.
func TestOp_MetricLabels_WhitelistAndStatus(t *testing.T) {
	mp, reader, _ := setupTest()
	tp := sdktrace.NewTracerProvider()
	op := NewOp(mp.Meter("test"), tp.Tracer("test"), "biz.op", WithMetricLabels("tenant"))

	_ = op.Observe(context.Background(), "Op", []attribute.KeyValue{
		attribute.String("tenant", "acme"),  // whitelisted → metric label
		attribute.String("order.id", "o-1"), // not whitelisted → span only
	}, func(ctx context.Context) error { return errors.New("boom") })

	dp := singleCounterDP(t, reader, "biz.op_total")
	if v, ok := dp.Attributes.Value("tenant"); !ok || v.AsString() != "acme" {
		t.Errorf("tenant label = %v (present=%v), want acme", v.AsString(), ok)
	}
	if _, ok := dp.Attributes.Value("order.id"); ok {
		t.Error("order.id leaked into metric labels")
	}
	if v, _ := dp.Attributes.Value("status"); v.AsString() != "error" {
		t.Errorf("status = %v, want error", v.AsString())
	}
}

// observable.Start + span.End(err) records error and sets status in one call.
func TestStart_EndWithError(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tracer := tp.Tracer("test")

	func() (err error) {
		_, span := Start(context.Background(), tracer, "biz.F")
		defer func() { span.End(err) }()
		return errors.New("kaboom")
	}()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status().Code)
	}
}

func singleCounterDP(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.DataPoint[int64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				dps := m.Data.(metricdata.Sum[int64]).DataPoints
				if len(dps) != 1 {
					t.Fatalf("metric %s: want 1 data point, got %d", name, len(dps))
				}
				return dps[0]
			}
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.DataPoint[int64]{}
}

func TestObserve_Panic(t *testing.T) {
	for _, withValue := range []bool{false, true} {
		name := "Observe"
		if withValue {
			name = "ObserveVal"
		}
		for _, payload := range []any{"boom", errors.New("boom"), &struct{ Code int }{42}} {
			t.Run(fmt.Sprintf("%s/%T", name, payload), func(t *testing.T) {
				reader := sdkmetric.NewManualReader()
				mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
				recorder := tracetest.NewSpanRecorder()
				tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				t.Cleanup(func() { _ = mp.Shutdown(context.Background()); _ = tp.Shutdown(context.Background()) })
				op := NewOp(mp.Meter("test"), tp.Tracer("test"), "panic_op", WithMetricLabels("operation"))
				attrs := []attribute.KeyValue{attribute.String("operation", "write"), attribute.String("request.id", "private")}
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					if withValue {
						_, _ = ObserveVal(op, context.Background(), "panic", attrs, func(context.Context) (int, error) { panic(payload) })
					} else {
						_ = op.Observe(context.Background(), "panic", attrs, func(context.Context) error { panic(payload) })
					}
				}()
				if recovered != payload {
					t.Fatalf("panic = %#v, want original %#v", recovered, payload)
				}
				spans := recorder.Ended()
				if len(spans) != 1 {
					t.Fatalf("ended spans = %d, want 1", len(spans))
				}
				if spans[0].Status().Code != codes.Error {
					t.Errorf("status = %v, want Error", spans[0].Status())
				}
				if events := spans[0].Events(); len(events) != 1 || events[0].Name != "exception" {
					t.Errorf("events = %v, want one exception", events)
				}
				var rm metricdata.ResourceMetrics
				if err := reader.Collect(context.Background(), &rm); err != nil {
					t.Fatal(err)
				}
				found := map[string]bool{}
				checkAttrs := func(attrs attribute.Set) {
					t.Helper()
					if v, _ := attrs.Value("status"); v.AsString() != "error" {
						t.Errorf("status = %v", v)
					}
					if v, _ := attrs.Value("operation"); v.AsString() != "write" {
						t.Errorf("operation = %v", v)
					}
					if _, ok := attrs.Value("request.id"); ok {
						t.Error("request.id leaked into metrics")
					}
				}
				for _, scope := range rm.ScopeMetrics {
					for _, m := range scope.Metrics {
						found[m.Name] = true
						switch data := m.Data.(type) {
						case metricdata.Sum[int64]:
							if len(data.DataPoints) != 1 || data.DataPoints[0].Value != 1 {
								t.Fatalf("counter = %v", data.DataPoints)
							}
							checkAttrs(data.DataPoints[0].Attributes)
						case metricdata.Histogram[float64]:
							if len(data.DataPoints) != 1 || data.DataPoints[0].Count != 1 || data.DataPoints[0].Sum <= 0 {
								t.Fatalf("histogram = %v", data.DataPoints)
							}
							checkAttrs(data.DataPoints[0].Attributes)
						}
					}
				}
				if !found["panic_op_total"] || !found["panic_op_seconds"] {
					t.Errorf("missing metrics: %v", found)
				}
			})
		}
	}
}
