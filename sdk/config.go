package signet

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Config holds unified observability configuration.
// It can be constructed directly or assembled by a configuration loader.
// Do not log or serialize it when OTLPHeaders contains credentials.
type Config struct {
	ServiceName    string
	ServiceVersion string
	InstanceID     string // defaults to os.Hostname() if empty
	Environment    string // "production", "staging", "development"

	OTLPEndpoint string            // shared OTLP URL: grpc://, grpcs://, http://, or https://
	OTLPTLS      *tls.Config       // required for TLS schemes, with non-empty RootCAs; optional client certificate
	OTLPHeaders  map[string]string // nil inherits exporter environment headers; non-nil replaces them

	Metrics MetricsConfig // Enabled defaults to false
	Tracing TracingConfig // Enabled defaults to false
}

// MetricsConfig controls metrics export.
// Metrics are pushed via OTLP when OTLPEndpoint is set.
type MetricsConfig struct {
	Enabled bool
	// Views customize metric names, aggregation and histogram buckets.
	Views        []sdkmetric.View `json:"-" yaml:"-"`
	PushInterval time.Duration    // OTLP periodic reader interval; 0 = default (15s)
	// Readers supplies additional metric readers programmatically. Each reader
	// must be non-nil and is owned and shut down by Signet after initialization.
	// Required when metrics are enabled without OTLPEndpoint. Views do not count.
	Readers []sdkmetric.Reader `json:"-" yaml:"-"`
}

// TracingConfig controls OTLP trace export.
type TracingConfig struct {
	Enabled      bool
	SampleRatio  *float64      // nil defaults to 1; explicit 0 disables sampling; valid range [0, 1]
	BatchTimeout time.Duration // batch export interval; 0 = default (5s)
	MaxBatchSize int           // max spans per batch; 0 = min(512, resolved MaxQueueSize)
	MaxQueueSize int           // in-memory queue capacity; 0 = default (2048)
}

// TLSConfigFromFiles loads server trust and optional client credentials into one
// TLS configuration, usable with Config.OTLPTLS or lower-level providers.
// caFile is required. Supply certFile and keyFile together only for mTLS.
func TLSConfigFromFiles(caFile, certFile, keyFile string) (*tls.Config, error) {
	if caFile == "" {
		return nil, fmt.Errorf("signet: OTLP CA file path is required")
	}
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("signet: OTLP TLS CertFile and KeyFile must be supplied together")
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("signet: read OTLP CA: %w", err)
	}
	result.RootCAs = x509.NewCertPool()
	if !result.RootCAs.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("signet: OTLP CA file contains no valid certificates")
	}

	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("signet: load OTLP client certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{cert}
	}
	return result, nil
}
