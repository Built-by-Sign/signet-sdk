package runtime

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegister(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test-runtime")

	if err := Register(meter); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}

	expected := []string{"process.runtime.go.goroutines", "process.runtime.go.mem.heap_alloc", "process.runtime.go.mem.sys"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing metric %q", name)
		}
	}
}

func TestRegisterHeartbeat(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test-runtime")

	if err := RegisterHeartbeat(meter, HeartbeatConfig{}); err != nil {
		t.Fatalf("RegisterHeartbeat failed: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	found := false
	now := float64(time.Now().Unix())
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != HeartbeatMetricName {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[float64])
			if !ok || len(gauge.DataPoints) != 1 {
				t.Fatalf("unexpected heartbeat data: %#v", m.Data)
			}
			dp := gauge.DataPoints[0]
			if dp.Value <= 0 || dp.Value > now {
				t.Fatalf("heartbeat value = %v, want a recent unix timestamp", dp.Value)
			}
			if dp.Attributes.Len() != 0 {
				t.Fatalf("heartbeat should have no data point attributes, got %v", dp.Attributes)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("missing metric %q", HeartbeatMetricName)
	}
}
