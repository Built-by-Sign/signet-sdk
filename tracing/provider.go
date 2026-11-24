package tracing

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/built-by-sign/signet-sdk/otlp"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ProviderConfig holds configuration for the trace provider.
type ProviderConfig struct {
	OTLPEndpoint string
	OTLPHeaders  map[string]string // nil inherits exporter environment headers; non-nil replaces them
	TLS          *tls.Config
	SampleRatio  *float64      // nil defaults to 1; explicit 0 disables sampling; valid range [0, 1]
	BatchTimeout time.Duration // Batch timeout; 0 defaults to 5s
	MaxBatchSize int           // Batch size; 0 defaults to min(512, resolved MaxQueueSize)
	MaxQueueSize int           // Queue size; 0 defaults to 2048
}

// NewTracerProvider creates an OTel TracerProvider with OTLP HTTP or gRPC exporter.
func NewTracerProvider(res *resource.Resource, cfg ProviderConfig) (*sdktrace.TracerProvider, error) {
	provider, _, err := NewTracerProviderWithConfig(res, cfg)
	return provider, err
}

// NewTracerProviderWithConfig also returns the resolved configuration on success.
// Defaults and validation are identical to NewTracerProvider. SampleRatio is copied.
func NewTracerProviderWithConfig(res *resource.Resource, cfg ProviderConfig) (*sdktrace.TracerProvider, ProviderConfig, error) {
	ratio := 1.0
	if cfg.SampleRatio != nil {
		ratio = *cfg.SampleRatio
	}
	cfg.SampleRatio = &ratio
	if math.IsNaN(ratio) || ratio < 0 || ratio > 1 {
		return nil, ProviderConfig{}, fmt.Errorf("tracing: SampleRatio must be between 0 and 1, got %v", ratio)
	}
	if cfg.BatchTimeout == 0 {
		cfg.BatchTimeout = 5 * time.Second
	}
	if cfg.MaxQueueSize == 0 {
		cfg.MaxQueueSize = 2048
	}
	if cfg.MaxBatchSize == 0 {
		cfg.MaxBatchSize = min(512, cfg.MaxQueueSize)
	}

	if cfg.BatchTimeout <= 0 || cfg.MaxBatchSize <= 0 || cfg.MaxQueueSize <= 0 || cfg.MaxBatchSize > cfg.MaxQueueSize {
		return nil, ProviderConfig{}, fmt.Errorf("tracing: positive batch settings with MaxBatchSize <= MaxQueueSize are required")
	}
	headers, err := otlp.ResolveHeaders(cfg.OTLPHeaders)
	if err != nil {
		return nil, ProviderConfig{}, err
	}
	cfg.OTLPHeaders = headers

	transport, err := otlp.ResolveTLS(cfg.OTLPEndpoint, cfg.TLS)
	if err != nil {
		return nil, ProviderConfig{}, err
	}
	exporter, err := newExporter(cfg, transport)
	if err != nil {
		return nil, ProviderConfig{}, err
	}

	batchOpts := []sdktrace.BatchSpanProcessorOption{
		sdktrace.WithBatchTimeout(cfg.BatchTimeout),
		sdktrace.WithMaxExportBatchSize(cfg.MaxBatchSize),
		sdktrace.WithMaxQueueSize(cfg.MaxQueueSize),
	}

	cfg.TLS = transport
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter, batchOpts...),
		sdktrace.WithSampler(buildSampler(*cfg.SampleRatio)),
	), cfg, nil
}

func buildSampler(rate float64) sdktrace.Sampler {
	switch rate {
	case 0:
		return sdktrace.NeverSample()
	case 1:
		return sdktrace.AlwaysSample()
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))
	}
}

func newExporter(cfg ProviderConfig, transport *tls.Config) (sdktrace.SpanExporter, error) {
	endpoint, err := otlp.ParseEndpoint(cfg.OTLPEndpoint)
	if err != nil {
		return nil, err
	}
	headers := cfg.OTLPHeaders
	if endpoint.HTTP {
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint),
			otlptracehttp.WithURLPath(endpoint.BasePath + "/v1/traces"),
		}
		if headers != nil {
			opts = append(opts, otlptracehttp.WithHeaders(headers))
		}
		if transport == nil {
			opts = append(opts, otlptracehttp.WithInsecure())
		} else {
			opts = append(opts, otlptracehttp.WithTLSClientConfig(transport))
		}
		return otlptracehttp.New(context.Background(), opts...)
	}
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint.Host),
	}
	if headers != nil {
		opts = append(opts, otlptracegrpc.WithHeaders(headers))
	}
	if transport == nil {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else {
		opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(transport)))
	}
	return otlptracegrpc.New(context.Background(), opts...)
}
