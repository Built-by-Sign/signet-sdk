package kratos

import (
	"context"
	"strings"
	"testing"

	signet "github.com/built-by-sign/signet-sdk"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestSetup_MetricsRequireReaderOrEndpoint(t *testing.T) {
	original := otel.GetMeterProvider()
	obs, err := Setup(signet.Config{
		ServiceName: "test-svc",
		Metrics:     signet.MetricsConfig{Enabled: true},
	})
	if obs != nil {
		_ = obs.Close()
		t.Fatal("Setup succeeded with only the automatic histogram view")
	}
	if err == nil || !strings.Contains(err.Error(), "Config.Metrics.Readers") {
		t.Fatalf("Setup error = %v, want explicit reader requirement", err)
	}
	if otel.GetMeterProvider() != original {
		t.Fatal("failed Setup changed the global meter provider")
	}
}

func TestSetup_ExplicitReader(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	original := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(original) })
	obs, err := Setup(signet.Config{
		ServiceName: "test-svc",
		Metrics:     signet.MetricsConfig{Enabled: true, Readers: []sdkmetric.Reader{reader}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = obs.Close() })
	if obs.MetricsMiddleware() == nil {
		t.Fatal("metrics middleware is missing")
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "service_heartbeat_unix_seconds" {
				return
			}
		}
	}
	t.Fatal("explicit reader did not collect the service heartbeat")
}

func TestSetup_MetricsDisabledNeedsNoEndpoint(t *testing.T) {
	obs, err := Setup(signet.Config{ServiceName: "test-svc"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = obs.Close() })
	if obs.Core().MeterProvider() != nil || obs.MetricsMiddleware() != nil {
		t.Fatal("metrics were created while disabled")
	}
}
