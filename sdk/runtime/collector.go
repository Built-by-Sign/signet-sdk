// Package runtime registers Go runtime metrics with the OTel MeterProvider.
// Uses "process." prefix to avoid conflicts with the default Prometheus Go collector.
package runtime

import (
	"context"
	"runtime"

	"go.opentelemetry.io/otel/metric"
)

// Register registers Go runtime observable gauges with the given Meter.
func Register(meter metric.Meter) error {
	if _, err := meter.Int64ObservableGauge("process.runtime.go.goroutines",
		metric.WithDescription("Number of goroutines"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(runtime.NumGoroutine()))
			return nil
		}),
	); err != nil {
		return err
	}

	heapAlloc, err := meter.Int64ObservableGauge("process.runtime.go.mem.heap_alloc",
		metric.WithDescription("Bytes of allocated heap objects"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	sys, err := meter.Int64ObservableGauge("process.runtime.go.mem.sys",
		metric.WithDescription("Total bytes of memory obtained from the OS"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	// One callback for both gauges: ReadMemStats is a stop-the-world
	// operation, so read it once per collection cycle.
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		o.ObserveInt64(heapAlloc, int64(m.Alloc))
		o.ObserveInt64(sys, int64(m.Sys))
		return nil
	}, heapAlloc, sys)
	return err
}
