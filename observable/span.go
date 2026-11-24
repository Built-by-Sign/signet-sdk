package observable

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span wraps trace.Span so End can record an error in one call.
//
// All other trace.Span methods (SetAttributes, AddEvent, ...) are promoted from
// the embedded span.
//
// Note: because End's signature differs from trace.Span.End, Span does NOT
// implement trace.Span — pass the embedded .Span where a trace.Span is required.
// Likewise, to end without an error (options only), call span.Span.End(opts...);
// span.End() with no error argument does not compile.
type Span struct{ trace.Span }

// Start begins a span and returns it wrapped for one-line error-aware End.
// Pair End with a named return value:
//
//	func F(ctx context.Context) (err error) {
//	    ctx, span := observable.Start(ctx, tracer, "biz.F")
//	    defer func() { span.End(err) }()
//	    span.SetAttributes(attribute.String("id", id)) // high-card detail OK on spans
//	    // ...
//	}
func Start(ctx context.Context, tracer trace.Tracer, name string, opts ...trace.SpanStartOption) (context.Context, Span) {
	ctx, span := tracer.Start(ctx, name, opts...)
	return ctx, Span{span}
}

// End finishes the span. When err is non-nil it records the error and sets the
// span status to Error; otherwise it just ends the span.
func (s Span) End(err error, opts ...trace.SpanEndOption) {
	endSpan(s.Span, err, opts...)
}

// EndSpan ends a raw trace.Span (e.g. one from tracer.Start directly),
// recording err as the span's error/status when non-nil:
//
//	ctx, span := tracer.Start(ctx, "biz.F")
//	defer func() { observable.EndSpan(span, err) }()
func EndSpan(span trace.Span, err error, opts ...trace.SpanEndOption) {
	endSpan(span, err, opts...)
}

func endSpan(span trace.Span, err error, opts ...trace.SpanEndOption) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End(opts...)
}
