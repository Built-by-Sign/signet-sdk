package logging

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// WrapHandler adds service identity and per-call trace context to h.
// A non-empty environment adds deployment.environment.name.
// Configure formatting, output and level on h before wrapping. Apply groups
// after wrapping to keep the injected fields outside those groups; existing
// groups on h cannot be undone. Bound LogValuers are resolved when attributes
// are attached, including values in nested groups. A nil handler panics.
func WrapHandler(h slog.Handler, serviceName, serviceVersion, instanceID, environment string) slog.Handler {
	if h == nil {
		panic("signet/logging: nil handler")
	}
	attrs := []slog.Attr{
		slog.String(KeyService, serviceName),
		slog.String(KeyVersion, serviceVersion),
		slog.String(KeyServiceID, instanceID),
	}
	if environment != "" {
		attrs = append(attrs, slog.String("deployment.environment.name", environment))
	}
	return &traceContextHandler{base: h.WithAttrs(attrs)}
}

// traceContextHandler injects trace.id/span.id from the active span at the ROOT
// of every record, then replays any user-applied groups/attrs on top. Injecting
// at the root (rather than via rec.AddAttrs) keeps trace.id/span.id top-level
// even when the logger has an open WithGroup, for node-agent parsing.
type traceContextHandler struct {
	base slog.Handler   // root handler (service fields applied), no user groups
	goas []groupOrAttrs // user-applied WithGroup/WithAttrs, replayed after trace
}

// groupOrAttrs holds either a group name or a set of attributes (exactly one).
type groupOrAttrs struct {
	group string
	attrs []slog.Attr
}

func (h *traceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *traceContextHandler) Handle(ctx context.Context, rec slog.Record) error {
	hd := h.base
	// trace.id/span.id at the root, before replaying user groups/attrs.
	// Omitted entirely when no span is active.
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		hd = hd.WithAttrs([]slog.Attr{
			slog.String(KeyTraceID, sc.TraceID().String()),
			slog.String(KeySpanID, sc.SpanID().String()),
		})
	}
	for _, goa := range h.goas {
		if goa.group != "" {
			hd = hd.WithGroup(goa.group)
		} else {
			hd = hd.WithAttrs(goa.attrs)
		}
	}
	return hd.Handle(ctx, rec)
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.withGoa(groupOrAttrs{attrs: resolveAttrs(attrs)})
}

// Resolve a private copy so replaying bound attributes does not re-evaluate
// LogValuers. Resolve does not descend into groups, so handle those recursively.
func resolveAttrs(attrs []slog.Attr) []slog.Attr {
	resolved := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		if attr.Value.Kind() == slog.KindGroup {
			attr.Value = slog.GroupValue(resolveAttrs(attr.Value.Group())...)
		}
		resolved[i] = attr
	}
	return resolved
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.withGoa(groupOrAttrs{group: name})
}

func (h *traceContextHandler) withGoa(goa groupOrAttrs) *traceContextHandler {
	goas := make([]groupOrAttrs, len(h.goas)+1)
	copy(goas, h.goas)
	goas[len(h.goas)] = goa
	return &traceContextHandler{base: h.base, goas: goas}
}
