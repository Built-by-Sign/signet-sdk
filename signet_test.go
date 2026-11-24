package signet

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

var testTime = mustParseTime("2026-04-09T00:00:00Z")

func floatPtr(value float64) *float64 { return &value }

func TestInit_MetricsOnly(t *testing.T) {
	obs, err := Init(Config{
		ServiceName: "test-svc",
		Metrics:     MetricsConfig{Enabled: true, Readers: []sdkmetric.Reader{sdkmetric.NewManualReader()}},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer func() { _ = obs.Close() }()

	if obs.MeterProvider() == nil {
		t.Fatal("MeterProvider should not be nil when metrics enabled")
	}
	if obs.TracerProvider() != nil {
		t.Fatal("TracerProvider should be nil when tracing disabled")
	}
}

func TestInit_Disabled(t *testing.T) {
	obs, err := Init(Config{
		ServiceName: "test-svc",
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer func() { _ = obs.Close() }()

	if obs.MeterProvider() != nil {
		t.Fatal("MeterProvider should be nil when metrics disabled")
	}
	if obs.TracerProvider() != nil {
		t.Fatal("TracerProvider should be nil when tracing disabled")
	}
}

func TestInit_MetricViewDoesNotReplaceReader(t *testing.T) {
	original := otel.GetMeterProvider()
	obs, err := Init(Config{ServiceName: "svc", Metrics: MetricsConfig{Enabled: true,
		Views: []sdkmetric.View{sdkmetric.NewView(
			sdkmetric.Instrument{Name: "latency"}, sdkmetric.Stream{Name: "latency_seconds"})},
	}})
	if obs != nil {
		_ = obs.Close()
		t.Fatal("Init returned an instance without an explicit reader or endpoint")
	}
	if err == nil || !strings.Contains(err.Error(), "Config.Metrics.Readers") {
		t.Fatalf("Init error = %v, want explicit reader requirement", err)
	}
	if otel.GetMeterProvider() != original {
		t.Fatal("invalid configuration replaced the global meter provider")
	}
}

func TestInit_ExplicitReadersWithView(t *testing.T) {
	first, second := sdkmetric.NewManualReader(), sdkmetric.NewManualReader()
	original := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(original) })
	obs, err := Init(Config{
		ServiceName: "svc",
		Metrics: MetricsConfig{Enabled: true, Readers: []sdkmetric.Reader{first, second},
			Views: []sdkmetric.View{sdkmetric.NewView(
				sdkmetric.Instrument{Name: "requests"}, sdkmetric.Stream{Name: "renamed_requests"})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = obs.Close() })
	counter, err := obs.MeterProvider().Meter("test").Int64Counter("requests")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 3)
	for _, reader := range []*sdkmetric.ManualReader{first, second} {
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name == "renamed_requests" {
					sum, ok := metric.Data.(metricdata.Sum[int64])
					if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 3 {
						t.Fatalf("unexpected counter data: %v", metric.Data)
					}
					found = true
				}
			}
		}
		if !found {
			t.Fatal("reader did not collect the counter transformed by the view")
		}
	}
	if err := obs.Close(); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*sdkmetric.ManualReader{first, second} {
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
			t.Fatalf("reader error after Close = %v, want ErrReaderShutdown", err)
		}
	}
}

func TestInit_ValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing service name", Config{}},
		{"invalid header", Config{ServiceName: "svc", OTLPHeaders: map[string]string{"Authorization": "Basic secret\r\nInjected: value"}}},
		{"unknown scheme", Config{ServiceName: "svc", OTLPEndpoint: "ftp://localhost:4317"}},
		{"missing scheme", Config{ServiceName: "svc", OTLPEndpoint: "localhost:4317"}},
		{"TLS missing endpoint", Config{ServiceName: "svc", OTLPTLS: &tls.Config{}}},
		{"tracing without endpoint", Config{ServiceName: "svc", Tracing: TracingConfig{Enabled: true}}},
		{"metrics without endpoint or reader", Config{ServiceName: "svc", Metrics: MetricsConfig{Enabled: true}}},
		{"nil metrics reader", Config{ServiceName: "svc", Metrics: MetricsConfig{Enabled: true, Readers: []sdkmetric.Reader{nil}}}},
		{"typed nil metrics reader", Config{ServiceName: "svc", Metrics: MetricsConfig{Enabled: true, Readers: []sdkmetric.Reader{(*sdkmetric.ManualReader)(nil)}}}},
		{"NaN sample ratio", Config{ServiceName: "svc", OTLPEndpoint: "grpc://localhost:4317", Tracing: TracingConfig{Enabled: true, SampleRatio: floatPtr(math.NaN())}}},
		{"infinite sample ratio", Config{ServiceName: "svc", OTLPEndpoint: "grpc://localhost:4317", Tracing: TracingConfig{Enabled: true, SampleRatio: floatPtr(math.Inf(1))}}},
		{"negative sample ratio", Config{ServiceName: "svc", OTLPEndpoint: "grpc://localhost:4317", Tracing: TracingConfig{Enabled: true, SampleRatio: floatPtr(-0.1)}}},
		{"sample ratio above one", Config{ServiceName: "svc", OTLPEndpoint: "grpc://localhost:4317", Tracing: TracingConfig{Enabled: true, SampleRatio: floatPtr(1.1)}}},
		{"plaintext mTLS", Config{ServiceName: "svc", OTLPEndpoint: "grpc://localhost:4317", OTLPTLS: &tls.Config{Certificates: []tls.Certificate{{}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := Init(tc.cfg)
			if err == nil {
				t.Fatal("Init should fail on invalid config")
			}
			if obs != nil {
				t.Fatal("Signet instance should be nil on error")
			}
		})
	}
}

func TestInit_ConfigAccessor(t *testing.T) {
	cfg := Config{
		ServiceName:    "test-svc",
		ServiceVersion: "1.0.0",
		Environment:    "testing",
	}
	obs, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer func() { _ = obs.Close() }()

	got := obs.Config()
	if got.ServiceName != "test-svc" {
		t.Fatalf("expected ServiceName 'test-svc', got %q", got.ServiceName)
	}
	if got.Environment != "testing" {
		t.Fatalf("expected Environment 'testing', got %q", got.Environment)
	}
	if got.InstanceID == "" {
		t.Fatal("expected InstanceID to default to the hostname")
	}
}

func TestWithLoggingEnrichesSuppliedHandler(t *testing.T) {
	originalDefault, originalWriter, originalFlags := slog.Default(), log.Writer(), log.Flags()
	obs, err := Init(Config{ServiceName: "svc", ServiceVersion: "1.2", InstanceID: "node-1", Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	if slog.Default() != originalDefault || log.Writer() != originalWriter || log.Flags() != originalFlags {
		t.Fatal("Init changed global logging")
	}
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	base := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{
		Level: level, AddSource: true,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			return a
		},
	})).With("application", "checkout")
	logger := slog.New(obs.WithLogging(base.Handler())).With("bound", true).WithGroup("request").With("route", "/pay")
	tid, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	sid, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}))
	logger.DebugContext(ctx, "request", "user", "alice")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"message": "request", "level": "DEBUG", "application": "checkout", "service.name": "svc", "service.version": "1.2", "service.id": "node-1", "deployment.environment.name": "test", "trace.id": tid.String(), "span.id": sid.String()} {
		if record[key] != want {
			t.Fatalf("%s = %v, want %s", key, record[key], want)
		}
	}
	group := record["request"].(map[string]any)
	if group["route"] != "/pay" || group["user"] != "alice" || group["trace.id"] != nil || record["bound"] != true {
		t.Fatalf("incorrect grouping: %v", record)
	}
	if record["time"] == nil || record["source"] == nil || record["ts"] != nil || record["caller"] != nil {
		t.Fatalf("wrapper changed handler format: %v", record)
	}
	output.Reset()
	level.Set(slog.LevelWarn)
	logger.InfoContext(ctx, "filtered")
	if output.Len() != 0 {
		t.Fatal("wrapper ignored dynamic level")
	}
	logger.Warn("no context")
	record = nil
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["trace.id"] != nil || record["span.id"] != nil {
		t.Fatal("trace leaked to context-free call")
	}
	output.Reset()
	base.WarnContext(ctx, "original")
	record = nil
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["service.id"] != nil || record["trace.id"] != nil {
		t.Fatal("original logger was modified")
	}
	if slog.Default() != originalDefault || log.Writer() != originalWriter || log.Flags() != originalFlags {
		t.Fatal("WithLogging changed global logging")
	}
}

func TestWithLoggingInstancesStayIndependent(t *testing.T) {
	var output bytes.Buffer
	h := slog.NewTextHandler(&output, nil)
	for _, name := range []string{"first", "second"} {
		obs, err := Init(Config{ServiceName: name})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = obs.Close() })
		output.Reset()
		slog.New(obs.WithLogging(h)).Info("hello")
		line := output.String()
		if !strings.Contains(line, "service.name="+name) || !strings.Contains(line, "service.id="+obs.Config().InstanceID) {
			t.Fatalf("missing service identity: %s", line)
		}
		if strings.Count(line, "service.name=") != 1 || strings.Contains(line, "deployment.environment.name=") {
			t.Fatalf("unexpected identity fields: %s", line)
		}
	}
}

func TestWithLoggingNilHandlerPanics(t *testing.T) {
	obs, err := Init(Config{ServiceName: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	defer func() {
		if recover() == nil {
			t.Fatal("nil handler should panic")
		}
	}()
	obs.WithLogging(nil)
}

func TestInit_RegistersHeartbeatMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()

	obs, err := Init(Config{
		ServiceName: "test-svc",
		InstanceID:  "node-1",
		Metrics:     MetricsConfig{Enabled: true, Readers: []sdkmetric.Reader{reader}},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer func() { _ = obs.Close() }()

	if obs.MeterProvider() == nil {
		t.Fatal("MeterProvider should not be nil when metrics enabled")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "service_heartbeat_unix_seconds" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected service heartbeat metric to be registered")
	}
}

func TestCloseRunsCleanupsOnce(t *testing.T) {
	calls := 0
	obs := &Signet{cleanups: []func(context.Context) error{
		func(context.Context) error {
			calls++
			return nil
		},
	}}

	if err := obs.CloseContext(context.Background()); err != nil {
		t.Fatalf("CloseContext() error = %v", err)
	}
	if err := obs.Close(); err != nil {
		t.Fatalf("Close() after CloseContext() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", calls)
	}
}

func mustParseTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func TestInitValidatesAndCopiesTLSConfig(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	callback := func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil }
	for _, tc := range []struct {
		name      string
		transport *tls.Config
		wantErr   bool
	}{
		{"TLS missing config", nil, true},
		{"TLS customized", &tls.Config{RootCAs: roots, ServerName: "collector.internal", InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}, false},
		{"TLS client certificate", &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{{}}}, false},
		{"TLS client callback", &tls.Config{RootCAs: roots, GetClientCertificate: callback}, false},
		{"custom TLS with client certificate", &tls.Config{RootCAs: roots, ServerName: "collector.internal", InsecureSkipVerify: true, Certificates: []tls.Certificate{{}}}, false},
		{"TLS empty config", &tls.Config{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := Init(Config{ServiceName: "test", OTLPEndpoint: "grpcs://localhost:4317", OTLPTLS: tc.transport})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if obs == nil {
				return
			}
			defer obs.Close()
			got := obs.Config()
			original := tc.transport
			if got.OTLPTLS == original {
				t.Fatal("TLS configuration must be cloned")
			}
			if got.OTLPTLS.ServerName != original.ServerName || got.OTLPTLS.InsecureSkipVerify != original.InsecureSkipVerify || got.OTLPTLS.MinVersion != original.MinVersion || len(got.OTLPTLS.Certificates) != len(original.Certificates) || (got.OTLPTLS.GetClientCertificate == nil) != (original.GetClientCertificate == nil) {
				t.Fatal("custom TLS settings lost")
			}
		})
	}
}

func TestTLSFileHelpers(t *testing.T) {
	if _, err := TLSConfigFromFiles("", "", ""); err == nil {
		t.Fatal("empty CA file path accepted")
	}
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	certificate := server.TLS.Certificates[0]
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0600); err != nil {
		t.Fatal(err)
	}
	serverTLS, err := TLSConfigFromFiles(certFile, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if serverTLS.RootCAs == nil || len(serverTLS.Certificates) != 0 {
		t.Fatal("unexpected server TLS credentials")
	}
	combined, err := TLSConfigFromFiles(certFile, certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if combined.RootCAs == nil || len(combined.Certificates) != 1 {
		t.Fatal("mTLS helper must load CA and client credentials")
	}
	for _, paths := range [][3]string{{"", certFile, keyFile}, {certFile, "", keyFile}, {certFile, certFile, ""}, {certFile, certFile, certFile}} {
		if _, err := TLSConfigFromFiles(paths[0], paths[1], paths[2]); err == nil {
			t.Fatal("invalid mTLS files accepted")
		}
	}
	if _, err := TLSConfigFromFiles(keyFile, "", ""); err == nil {
		t.Fatal("invalid CA accepted")
	}
	if _, err := TLSConfigFromFiles(certFile+".missing", "", ""); err == nil {
		t.Fatal("missing CA accepted")
	}
}

func TestInitRetainsProviderResolvedConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Content-Type", "application/x-protobuf") }))
	defer server.Close()
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			input := Config{ServiceName: "resolved", OTLPEndpoint: server.URL, Metrics: MetricsConfig{Enabled: true}, Tracing: TracingConfig{Enabled: true}}
			if explicit {
				input.Metrics.PushInterval = time.Hour
				input.Tracing = TracingConfig{Enabled: true, SampleRatio: floatPtr(0), BatchTimeout: time.Second, MaxQueueSize: 4}
			}
			obs, err := Init(input)
			if err != nil {
				t.Fatal(err)
			}
			defer obs.Close()
			got := obs.Config()
			if explicit {
				if got.Metrics.PushInterval != time.Hour || got.Tracing.BatchTimeout != time.Second || got.Tracing.MaxQueueSize != 4 || got.Tracing.MaxBatchSize != 4 || *got.Tracing.SampleRatio != 0 {
					t.Fatalf("resolved=%+v", got)
				}
				*input.Tracing.SampleRatio = 1
				if *obs.Config().Tracing.SampleRatio != 0 {
					t.Fatal("sample ratio aliases input")
				}
			} else {
				if got.Metrics.PushInterval != 15*time.Second || got.Tracing.BatchTimeout != 5*time.Second || got.Tracing.MaxQueueSize != 2048 || got.Tracing.MaxBatchSize != 512 || *got.Tracing.SampleRatio != 1 {
					t.Fatalf("resolved=%+v", got)
				}
				if input.Tracing.SampleRatio != nil || input.Metrics.PushInterval != 0 {
					t.Fatal("input mutated")
				}
			}
		})
	}
}

func TestInitDisabledSignalsSkipResolution(t *testing.T) {
	cfg := Config{ServiceName: "disabled", Metrics: MetricsConfig{PushInterval: -1}, Tracing: TracingConfig{SampleRatio: floatPtr(math.NaN()), BatchTimeout: -1}}
	obs, err := Init(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	got := obs.Config()
	if obs.MeterProvider() != nil || obs.TracerProvider() != nil || got.Metrics.PushInterval != -1 || got.Tracing.BatchTimeout != -1 || !math.IsNaN(*got.Tracing.SampleRatio) {
		t.Fatal("disabled signals resolved")
	}
}
