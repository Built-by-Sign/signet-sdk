package runtime

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

const HeartbeatMetricName = "service_heartbeat_unix_seconds"

type HeartbeatConfig struct{}

// RegisterHeartbeat registers a service heartbeat gauge that reports the
// current Unix timestamp for the running service instance.
func RegisterHeartbeat(meter metric.Meter, cfg HeartbeatConfig) error {
	gauge, err := meter.Float64ObservableGauge(
		HeartbeatMetricName,
		metric.WithDescription("Unix timestamp of the latest heartbeat from this service instance"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveFloat64(gauge, float64(time.Now().Unix()))
		return nil
	}, gauge)
	return err
}
