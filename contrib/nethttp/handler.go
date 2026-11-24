// Package nethttp integrates Signet with standard-library HTTP servers.
package nethttp

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type parentSpanKey struct{}

// NewHandler instruments handler with server spans and HTTP metrics, using the
// global providers installed by signet.Init unless overridden by opts.
//
// Wrap a http.ServeMux once, after registering its routes. The matched
// Request.Pattern supplies http.route before the server span ends; method and
// host prefixes are removed, while path wildcards remain intact. Handlers that
// do not populate Request.Pattern receive ordinary otelhttp instrumentation
// without a route attribute. The request URL is never used as a fallback.
//
// Options are forwarded to otelhttp.NewHandler. Filtered requests do not modify
// their parent span. Do not additionally wrap the result with otelhttp.NewHandler.
func NewHandler(handler http.Handler, operation string, opts ...otelhttp.Option) http.Handler {
	withRoute := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		parent, _ := r.Context().Value(parentSpanKey{}).(trace.SpanContext)
		defer func() {
			// An otelhttp filter can bypass span creation entirely.
			if !span.IsRecording() || span.SpanContext().Equal(parent) {
				return
			}
			if start := strings.IndexByte(r.Pattern, '/'); start >= 0 {
				span.SetAttributes(attribute.String("http.route", r.Pattern[start:]))
			}
		}()
		handler.ServeHTTP(w, r)
	})
	instrumented := otelhttp.NewHandler(withRoute, operation, opts...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), parentSpanKey{}, trace.SpanContextFromContext(r.Context()))
		request := r.WithContext(ctx)
		defer func() {
			// Preserve net/http's cleanup of multipart temporary files.
			if request.MultipartForm != nil {
				r.MultipartForm = request.MultipartForm
			}
		}()
		instrumented.ServeHTTP(w, request)
	})
}
