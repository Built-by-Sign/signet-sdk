package observable

type opConfig struct {
	buckets      []float64
	metricLabels []string
}

// OpOption configures an observable operation.
type OpOption func(*opConfig)

// WithBuckets sets the histogram bucket boundaries.
// Default: [0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1]
func WithBuckets(buckets ...float64) OpOption {
	return func(c *opConfig) { c.buckets = buckets }
}

// WithMetricLabels whitelists which attribute keys may become metric labels.
//
// By default metrics carry only an automatic status="ok"|"error" label (the
// operation is already encoded in the metric name), so high-cardinality
// attributes you pass to Observe/ObserveVal go to the span ONLY and never
// explode metric series. Opt in to extra metric dimensions here, and ONLY with
// low-cardinality keys (e.g. "operation", "tenant", "result_kind") — never IDs,
// account numbers, or other unbounded values.
func WithMetricLabels(keys ...string) OpOption {
	return func(c *opConfig) { c.metricLabels = keys }
}
