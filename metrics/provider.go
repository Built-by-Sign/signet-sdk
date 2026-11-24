package metrics

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/built-by-sign/signet-sdk/otlp"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// ProviderConfig holds configuration for the meter provider.
type ProviderConfig struct {
	OTLPEndpoint string
	OTLPHeaders  map[string]string // nil inherits exporter environment headers; non-nil replaces them
	TLS          *tls.Config
	PushInterval time.Duration      // PeriodicReader interval; 0 defaults to 15s, negatives are rejected
	Readers      []sdkmetric.Reader // Explicit readers, in addition to any OTLP reader
}

// NewMeterProvider creates an OTel MeterProvider.
// When cfg.OTLPEndpoint is set, metrics are pushed via OTLP HTTP or gRPC.
// extraOpts can be used to inject custom views (e.g. Kratos histogram bucket views).
// An endpoint or at least one explicit reader is required, regardless of extraOpts.
func NewMeterProvider(res *resource.Resource, cfg ProviderConfig, extraOpts ...sdkmetric.Option) (*sdkmetric.MeterProvider, error) {
	provider, _, err := NewMeterProviderWithConfig(res, cfg, extraOpts...)
	return provider, err
}

// NewMeterProviderWithConfig also returns the resolved configuration on success.
// Defaults and validation are identical to NewMeterProvider.
func NewMeterProviderWithConfig(res *resource.Resource, cfg ProviderConfig, extraOpts ...sdkmetric.Option) (*sdkmetric.MeterProvider, ProviderConfig, error) {
	if cfg.PushInterval == 0 {
		cfg.PushInterval = 15 * time.Second
	}

	if cfg.PushInterval < 0 {
		return nil, ProviderConfig{}, fmt.Errorf("metrics: PushInterval must be positive")
	}
	if cfg.OTLPEndpoint == "" && len(cfg.Readers) == 0 {
		return nil, ProviderConfig{}, fmt.Errorf("metrics: OTLPEndpoint or Readers is required")
	}
	// Validate before creating an exporter or registering any reader. A typed nil
	// pointer also satisfies the Reader interface but cannot collect metrics.
	for i, reader := range cfg.Readers {
		if reader == nil || (reflect.ValueOf(reader).Kind() == reflect.Ptr && reflect.ValueOf(reader).IsNil()) {
			return nil, ProviderConfig{}, fmt.Errorf("metrics: Readers[%d] must not be nil", i)
		}
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
	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
	}
	for _, reader := range cfg.Readers {
		opts = append(opts, sdkmetric.WithReader(reader))
	}

	// OTLP push reader — when endpoint is configured
	if cfg.OTLPEndpoint != "" {
		otlpExp, err := newExporter(cfg, transport)
		if err != nil {
			return nil, ProviderConfig{}, fmt.Errorf("create OTLP metric exporter: %w", err)
		}

		readerOpts := []sdkmetric.PeriodicReaderOption{
			sdkmetric.WithInterval(cfg.PushInterval),
		}
		opts = append(opts, sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(otlpExp, readerOpts...),
		))
	}

	opts = append(opts, extraOpts...)

	cfg.TLS = transport
	return sdkmetric.NewMeterProvider(opts...), cfg, nil
}

func newExporter(cfg ProviderConfig, transport *tls.Config) (sdkmetric.Exporter, error) {
	endpoint, err := otlp.ParseEndpoint(cfg.OTLPEndpoint)
	if err != nil {
		return nil, err
	}
	headers := cfg.OTLPHeaders
	if endpoint.HTTP {
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint),
			otlpmetrichttp.WithURLPath(endpoint.BasePath + "/v1/metrics"),
		}
		if headers != nil {
			opts = append(opts, otlpmetrichttp.WithHeaders(headers))
		}
		if transport == nil {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		} else {
			opts = append(opts, otlpmetrichttp.WithTLSClientConfig(transport))
		}
		return otlpmetrichttp.New(context.Background(), opts...)
	}
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(endpoint.Host),
	}
	if headers != nil {
		opts = append(opts, otlpmetricgrpc.WithHeaders(headers))
	}
	if transport == nil {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(transport)))
	}
	return otlpmetricgrpc.New(context.Background(), opts...)
}
