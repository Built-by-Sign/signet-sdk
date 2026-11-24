// Package logging defines standard structured log fields for observability.
// It is framework-agnostic — framework adapters convert these to their
// native log field formats.
package logging

// Standard field keys used across all services.
const (
	KeyTraceID    = "trace.id"
	KeySpanID     = "span.id"
	KeyServiceID  = "service.id"
	KeyService    = "service.name"
	KeyVersion    = "service.version"
	KeyTimestamp  = "ts"
	KeyCaller     = "caller"
	KeyLevel      = "level"
	KeyMessage    = "msg"
	KeyError      = "error"
	KeyOperation  = "operation"
	KeyLatency    = "latency"
)
