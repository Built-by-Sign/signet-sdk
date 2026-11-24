// Package signet provides unified, framework-agnostic observability initialization
// for Go services. Metrics and traces use OTLP HTTP or gRPC. WithLogging optionally
// enriches an application-supplied slog handler with service and trace identity.
//
// Quick start with a local plaintext OTLP receiver (no framework):
// The default Compose receiver requires TLS and Bearer credentials instead;
// see the Signet deployment repository for that configuration.
//
//	obs, err := signet.Init(signet.Config{
//	    ServiceName: "my-service", Environment: "development",
//	    OTLPEndpoint: "grpc://localhost:4317",
//	    Metrics: signet.MetricsConfig{Enabled: true},
//	    Tracing: signet.TracingConfig{Enabled: true},
//	})
//	if err != nil {
//	    return err
//	}
//	defer func() { _ = obs.Close() }()
//	logger := slog.New(obs.WithLogging(slog.NewJSONHandler(os.Stdout, nil)))
//	logger.InfoContext(ctx, "ready")
//
// After Init:
//   - WithLogging returns an enriched handler; Init leaves global loggers unchanged
//   - When metrics are enabled, Go runtime metrics and service heartbeat are registered
//   - Enabled providers are installed globally for otel.Meter() / otel.Tracer()
//   - Disabled signals leave the corresponding global provider unchanged
//
// With Kratos framework (import "github.com/built-by-sign/signet-sdk/contrib/kratos"):
//
//	kratosObs, err := kratossignet.Setup(cfg)
//	if err != nil {
//	    return err
//	}
//	mws := kratosObs.Middlewares()                 // tracing -> metrics, correct order
//	kratosObs.RegisterRoutes(srv)                  // registers /healthz + /readyz
//	httpMW := kratosObs.HTTPClientMiddleware()     // HTTP client trace propagation
//	dialOpts := kratosObs.GRPCDialOptions()        // gRPC client trace propagation
//
// Key notes:
//   - ServiceName must not be empty; it sets the service.name resource attribute
//   - Metrics and traces share endpoint and authentication settings, with separate exporters
//   - When enabled, tracing requires OTLPEndpoint; metrics require an endpoint or explicit Readers
//   - Invalid configuration returns an error instead of silently disabling signals
//   - On error, Init has already cleaned up any providers initialized so far
//   - Push mode has no Prometheus "up" metric; use service_heartbeat_unix_seconds for liveness
package signet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/built-by-sign/signet-sdk/health"
	siglogging "github.com/built-by-sign/signet-sdk/logging"
	sigmetrics "github.com/built-by-sign/signet-sdk/metrics"
	"github.com/built-by-sign/signet-sdk/otlp"
	sigruntime "github.com/built-by-sign/signet-sdk/runtime"
	sigtracing "github.com/built-by-sign/signet-sdk/tracing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// closeTimeout bounds Close. Callers needing another deadline use CloseContext.
const closeTimeout = 10 * time.Second

// Signet is the initialized observability instance.
// It exposes framework-agnostic OTel providers and HTTP handlers.
type Signet struct {
	cfg Config

	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	health         *health.Handler

	cleanups  []func(context.Context) error
	closeOnce sync.Once
	closeErr  error
}

// Init initializes signals whose configuration has Enabled set to true.
// Each provider resolves its signal defaults; Config returns the resolved settings.
// Disabled signals skip signal-specific validation and initialization, while shared
// transport settings are still validated. Metric views do not replace readers.
//
// Global OTel providers and the propagator are updated only after all providers
// initialize successfully. On failure, providers created so far are shut down.
//
// Initialize once at process startup. The returned Signet owns provider lifecycle;
// call Close for a 10-second shutdown timeout or CloseContext for a caller deadline.
// Close does not restore previous globals; the process is expected to exit afterward.
func Init(cfg Config) (*Signet, error) {
	headers, err := otlp.ResolveHeaders(cfg.OTLPHeaders)
	if err != nil {
		return nil, err
	}
	cfg.OTLPHeaders = headers
	if cfg.OTLPTLS != nil {
		cfg.OTLPTLS = cfg.OTLPTLS.Clone()
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID, _ = os.Hostname()
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	s := &Signet{
		cfg:    cfg,
		health: health.New(),
	}
	// fail shuts down anything initialized so far and reports err.
	fail := func(err error) (*Signet, error) {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		_ = s.CloseContext(ctx)
		return nil, err
	}

	res, err := buildResource(cfg)
	if err != nil {
		return fail(err)
	}

	// --- Metrics ---
	if cfg.Metrics.Enabled {
		meterOpts := []sdkmetric.Option{sdkmetric.WithView(cfg.Metrics.Views...)}
		mp, resolved, err := sigmetrics.NewMeterProviderWithConfig(res, sigmetrics.ProviderConfig{
			OTLPEndpoint: cfg.OTLPEndpoint,
			OTLPHeaders:  cfg.OTLPHeaders,
			TLS:          cfg.OTLPTLS,
			PushInterval: cfg.Metrics.PushInterval,
			Readers:      cfg.Metrics.Readers,
		}, meterOpts...)
		if err != nil {
			return fail(err)
		}
		s.cfg.Metrics.PushInterval = resolved.PushInterval
		s.meterProvider = mp
		s.cleanups = append(s.cleanups, mp.Shutdown)

		runtimeMeter := mp.Meter(cfg.ServiceName)
		if err := sigruntime.Register(runtimeMeter); err != nil {
			return fail(err)
		}
		if err := sigruntime.RegisterHeartbeat(runtimeMeter, sigruntime.HeartbeatConfig{}); err != nil {
			return fail(err)
		}
	}

	// --- Tracing ---
	if cfg.Tracing.Enabled {
		tp, resolved, err := sigtracing.NewTracerProviderWithConfig(res, sigtracing.ProviderConfig{
			OTLPEndpoint: cfg.OTLPEndpoint,
			OTLPHeaders:  cfg.OTLPHeaders,
			TLS:          cfg.OTLPTLS,
			SampleRatio:  cfg.Tracing.SampleRatio,
			BatchTimeout: cfg.Tracing.BatchTimeout,
			MaxBatchSize: cfg.Tracing.MaxBatchSize,
			MaxQueueSize: cfg.Tracing.MaxQueueSize,
		})
		if err != nil {
			return fail(err)
		}
		s.cfg.Tracing.SampleRatio = resolved.SampleRatio
		s.cfg.Tracing.BatchTimeout = resolved.BatchTimeout
		s.cfg.Tracing.MaxBatchSize = resolved.MaxBatchSize
		s.cfg.Tracing.MaxQueueSize = resolved.MaxQueueSize
		s.tracerProvider = tp
		s.cleanups = append(s.cleanups, tp.Shutdown)
	}

	// --- Global registration: only after all providers initialized successfully ---
	if s.meterProvider != nil {
		otel.SetMeterProvider(s.meterProvider)
	}
	if s.tracerProvider != nil {
		otel.SetTracerProvider(s.tracerProvider)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	return s, nil
}

func (s *Signet) WithLogging(h slog.Handler) slog.Handler {
	return siglogging.WrapHandler(h, s.cfg.ServiceName, s.cfg.ServiceVersion, s.cfg.InstanceID, s.cfg.Environment)
}

func validateConfig(cfg Config) error {
	if cfg.Metrics.Enabled {
		for i, view := range cfg.Metrics.Views {
			if view == nil {
				return fmt.Errorf("signet: Config.Metrics.Views[%d] must not be nil", i)
			}
		}
	}
	if cfg.ServiceName == "" {
		return errors.New("signet: Config.ServiceName is required")
	}
	if _, err := otlp.ResolveTLS(cfg.OTLPEndpoint, cfg.OTLPTLS); err != nil {
		return err
	}
	if cfg.Tracing.Enabled && cfg.OTLPEndpoint == "" {
		return errors.New("signet: Config.OTLPEndpoint is required when tracing is enabled")
	}
	if cfg.Metrics.Enabled && cfg.OTLPEndpoint == "" && len(cfg.Metrics.Readers) == 0 {
		return errors.New("signet: Config.OTLPEndpoint or Config.Metrics.Readers is required when metrics are enabled")
	}
	return nil
}

// CloseContext flushes and stops all providers in reverse initialization order,
// joining any errors. Repeated calls return the result of the first call. Use it
// when the caller needs to control the deadline or inspect flush failures, such
// as the last batch of spans being dropped.
func (s *Signet) CloseContext(ctx context.Context) error {
	s.closeOnce.Do(func() {
		var errs []error
		for i := len(s.cleanups) - 1; i >= 0; i-- {
			errs = append(errs, s.cleanups[i](ctx))
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

// Close flushes and stops all providers with a 10-second timeout.
// Use CloseContext when the caller needs to control the deadline.
func (s *Signet) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return s.CloseContext(ctx)
}

// --- Framework-agnostic accessors ---

// Config returns provider-resolved settings for enabled signals and the supplied
// settings for disabled signals. The result is a shallow copy; treat referenced
// values as read-only. It may include credentials in OTLPHeaders and must not be logged.
func (s *Signet) Config() Config { return s.cfg }

// MeterProvider returns the OTel MeterProvider, or nil if metrics are disabled.
func (s *Signet) MeterProvider() *sdkmetric.MeterProvider { return s.meterProvider }

// TracerProvider returns the OTel TracerProvider, or nil if tracing is disabled.
func (s *Signet) TracerProvider() *sdktrace.TracerProvider { return s.tracerProvider }

// HealthHandler returns the health check handler with /healthz and /readyz.
func (s *Signet) HealthHandler() *health.Handler { return s.health }
