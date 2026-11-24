// Package kratos provides Kratos (v3) framework integration for the Signet SDK.
//
// Usage:
//
//	obs, _ := kratos.Setup(cfg)
//	defer func() { _ = obs.Close() }()
//
//	logger := slog.New(obs.Core().WithLogging(slog.NewJSONHandler(os.Stdout, nil)))
//	mws := obs.Middlewares()
//	httpClientMW := obs.HTTPClientMiddleware()
//	dialOpts := obs.GRPCDialOptions()
//
// Tracing/metrics middleware come from the official kratos OTel contrib
// (github.com/go-kratos/kratos/contrib/otel/v3), which is built on the same
// OpenTelemetry version matrix as the Signet core.
package kratos

import (
	"context"
	signet "github.com/built-by-sign/signet/sdk"
	"github.com/built-by-sign/signet/sdk/health"

	otelmetrics "github.com/go-kratos/kratos/contrib/otel/v3/metrics"
	oteltracing "github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/middleware"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc"
)

// Observability wraps a framework-agnostic Signet instance and provides
// Kratos-specific middleware and helpers.
type Observability struct {
	core *signet.Signet
}

// Setup initializes Signet with the Kratos histogram view and returns a
// ready-to-use Kratos adapter.
func Setup(cfg signet.Config) (*Observability, error) {
	if cfg.Metrics.Enabled {
		metrics := cfg.Metrics
		metrics.Views = append(append([]sdkmetric.View(nil), metrics.Views...), HistogramMetricView())
		cfg.Metrics = metrics
	}
	core, err := signet.Init(cfg)
	if err != nil {
		return nil, err
	}
	return Wrap(core), nil
}

// Close flushes and stops all providers with the core SDK default timeout.
func (o *Observability) Close() error { return o.core.Close() }

// CloseContext flushes and stops all providers using the supplied context.
func (o *Observability) CloseContext(ctx context.Context) error {
	return o.core.CloseContext(ctx)
}

// Wrap creates a Kratos adapter around a Signet instance.
func Wrap(s *signet.Signet) *Observability {
	return &Observability{core: s}
}

// Core returns the underlying framework-agnostic Signet instance.
func (o *Observability) Core() *signet.Signet { return o.core }

// HealthHandler returns the health check handler (proxy to core).
func (o *Observability) HealthHandler() *health.Handler { return o.core.HealthHandler() }

// RegisterRoutes registers observability HTTP routes on a Kratos HTTP server:
//
//	/healthz  — liveness check (always 200)
//	/readyz   — readiness check (runs registered checkers)
func (o *Observability) RegisterRoutes(srv *khttp.Server) {
	h := o.core.HealthHandler()
	srv.Handle("/healthz", h.LiveHandler())
	srv.Handle("/readyz", h.ReadyHandler())
}

// TracingMiddleware returns the Kratos server-side tracing middleware.
// Returns nil if tracing is disabled.
func (o *Observability) TracingMiddleware() middleware.Middleware {
	tp := o.core.TracerProvider()
	if tp == nil {
		return nil
	}
	return oteltracing.Server(oteltracing.WithTracerProvider(tp))
}

// MetricsMiddleware returns the Kratos server-side metrics middleware.
// Returns nil if metrics are disabled.
func (o *Observability) MetricsMiddleware() middleware.Middleware {
	mp := o.core.MeterProvider()
	if mp == nil {
		return nil
	}
	meter := mp.Meter(o.core.Config().ServiceName)
	requests, err := otelmetrics.DefaultRequestsCounter(meter, otelmetrics.DefaultServerRequestsCounterName)
	if err != nil {
		otel.Handle(err)
	}
	seconds, err := otelmetrics.DefaultSecondsHistogram(meter, otelmetrics.DefaultServerSecondsHistogramName)
	if err != nil {
		otel.Handle(err)
	}
	var opts []otelmetrics.Option
	if requests != nil {
		opts = append(opts, otelmetrics.WithRequests(requests))
	}
	if seconds != nil {
		opts = append(opts, otelmetrics.WithSeconds(seconds))
	}
	return otelmetrics.Server(opts...)
}

// Middlewares returns all observability middleware in the correct order:
// tracing -> metrics.
// Nil middleware are excluded.
// Does not include recovery or auth — add those yourself.
// Append recovery after these middleware so recovered business panics are
// recorded as errors by both metrics and tracing.
func (o *Observability) Middlewares() []middleware.Middleware {
	var mws []middleware.Middleware
	if mw := o.TracingMiddleware(); mw != nil {
		mws = append(mws, mw)
	}
	if mw := o.MetricsMiddleware(); mw != nil {
		mws = append(mws, mw)
	}
	return mws
}

// HTTPClientMiddleware returns Kratos client-side tracing middleware for HTTP
// trace-context propagation. It returns a pass-through middleware when tracing
// is disabled, so callers can always install it unconditionally.
func (o *Observability) HTTPClientMiddleware() middleware.Middleware {
	tp := o.core.TracerProvider()
	if tp == nil {
		return func(next middleware.Handler) middleware.Handler { return next }
	}
	return oteltracing.Client(oteltracing.WithTracerProvider(tp))
}

// GRPCDialOptions returns gRPC dial options with OTel client interceptors
// for automatic trace context propagation across gRPC calls.
// Returns nil if tracing is disabled.
//
// In Kratos v3, pass these to the gRPC client via transport/grpc.WithOptions(...).
func (o *Observability) GRPCDialOptions() []grpc.DialOption {
	if o.core.TracerProvider() == nil {
		return nil
	}
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
}

// HistogramView returns an OTel meter option for the Kratos request latency
// histogram. For Signet initialization, use HistogramMetricView or Setup:
//
//	cfg.Metrics.Views = append(cfg.Metrics.Views, kratos.HistogramMetricView())
func HistogramView() sdkmetric.Option {
	return sdkmetric.WithView(HistogramMetricView())
}

// HistogramMetricView returns the Kratos request latency view for MetricsConfig.Views.
func HistogramMetricView() sdkmetric.View {
	return otelmetrics.DefaultSecondsHistogramView(otelmetrics.DefaultServerSecondsHistogramName)
}
