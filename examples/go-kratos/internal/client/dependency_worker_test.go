package client

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/propagation"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestDependencyWorkerClientPropagatesTraceContext(t *testing.T) {
    receivedTraceparent := ""
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        receivedTraceparent = r.Header.Get("traceparent")
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    provider := sdktrace.NewTracerProvider()
    previousProvider := otel.GetTracerProvider()
    previousPropagator := otel.GetTextMapPropagator()
    otel.SetTracerProvider(provider)
    otel.SetTextMapPropagator(propagation.TraceContext{})
    t.Cleanup(func() {
        otel.SetTracerProvider(previousProvider)
        otel.SetTextMapPropagator(previousPropagator)
        _ = provider.Shutdown(context.Background())
    })

    ctx, span := otel.Tracer("test").Start(context.Background(), "parent")
    defer span.End()
    if err := NewDependencyWorkerClient(server.URL).Call(ctx, "success"); err != nil {
        t.Fatalf("Call() error = %v", err)
    }
    if receivedTraceparent == "" {
        t.Fatal("expected traceparent header")
    }
}