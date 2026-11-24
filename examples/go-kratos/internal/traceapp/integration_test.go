package traceapp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	v1 "kratos-usage/api/trace/v1"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type testHarness struct {
	httpClient  v1.ValidationHTTPClient
	grpcClient  v1.ValidationClient
	workerCalls *atomic.Int32
	provider    *sdktrace.TracerProvider
	recorder    *tracetest.SpanRecorder
}

type countingWorker struct {
	v1.UnimplementedWorkerServer
	calls    *atomic.Int32
	delegate *WorkerService
}

func (w *countingWorker) Execute(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteReply, error) {
	w.calls.Add(1)
	return w.delegate.Execute(ctx, req)
}

func TestRunChainOverHTTP(t *testing.T) {
	h := newTestHarness(t)
	testChainCases(t, h, func(ctx context.Context, req *v1.RunChainRequest) error {
		_, err := h.httpClient.RunChain(ctx, req)
		return err
	})
}

func TestRunChainOverGRPC(t *testing.T) {
	h := newTestHarness(t)
	testChainCases(t, h, func(ctx context.Context, req *v1.RunChainRequest) error {
		_, err := h.grpcClient.RunChain(ctx, req)
		return err
	})
}

func TestRunLocalOverHTTP(t *testing.T) {
	h := newTestHarness(t)
	testLocalCases(t, h, func(ctx context.Context, req *v1.RunLocalRequest) error {
		_, err := h.httpClient.RunLocal(ctx, req)
		return err
	})
}

func TestRunLocalOverGRPC(t *testing.T) {
	h := newTestHarness(t)
	testLocalCases(t, h, func(ctx context.Context, req *v1.RunLocalRequest) error {
		_, err := h.grpcClient.RunLocal(ctx, req)
		return err
	})
}

func testChainCases(t *testing.T, h *testHarness, call func(context.Context, *v1.RunChainRequest) error) {
	t.Helper()
	cases := []struct {
		name        string
		a           v1.Outcome
		b           v1.Outcome
		wantErr     bool
		wantBCalls  int32
		wantBStatus codes.Code
	}{
		{name: "ok_ok", a: v1.Outcome_OUTCOME_OK, b: v1.Outcome_OUTCOME_OK, wantBCalls: 1, wantBStatus: codes.Ok},
		{name: "ok_error", a: v1.Outcome_OUTCOME_OK, b: v1.Outcome_OUTCOME_ERROR, wantErr: true, wantBCalls: 1, wantBStatus: codes.Error},
		{name: "error_nil", a: v1.Outcome_OUTCOME_ERROR, b: v1.Outcome_OUTCOME_UNSPECIFIED, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeCalls := h.workerCalls.Load()
			beforeSpans := len(h.recorder.Ended())
			ctx, root := h.provider.Tracer("test").Start(context.Background(), "test.root")
			rootTraceID := root.SpanContext().TraceID()
			err := call(ctx, &v1.RunChainRequest{A: tc.a, B: tc.b})
			root.End()
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got := h.workerCalls.Load() - beforeCalls; got != tc.wantBCalls {
				t.Fatalf("worker calls = %d, want %d", got, tc.wantBCalls)
			}

			business := businessSpans(h.recorder.Ended()[beforeSpans:])
			wantSpanCount := 1
			if tc.wantBCalls == 1 {
				wantSpanCount = 2
			}
			if len(business) != wantSpanCount {
				t.Fatalf("business spans = %d, want %d", len(business), wantSpanCount)
			}
			assertSpan(t, business, "service_a.execute", rootTraceID, outcomeStatus(tc.a))
			if tc.wantBCalls == 1 {
				assertSpan(t, business, "service_b.execute", rootTraceID, tc.wantBStatus)
			}
		})
	}
}

func testLocalCases(t *testing.T, h *testHarness, call func(context.Context, *v1.RunLocalRequest) error) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		a       v1.Outcome
		wantErr bool
	}{
		{name: "ok", a: v1.Outcome_OUTCOME_OK},
		{name: "error", a: v1.Outcome_OUTCOME_ERROR, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeCalls := h.workerCalls.Load()
			beforeSpans := len(h.recorder.Ended())
			ctx, root := h.provider.Tracer("test").Start(context.Background(), "test.root")
			rootTraceID := root.SpanContext().TraceID()
			err := call(ctx, &v1.RunLocalRequest{A: tc.a})
			root.End()
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got := h.workerCalls.Load() - beforeCalls; got != 0 {
				t.Fatalf("worker calls = %d, want 0", got)
			}
			business := businessSpans(h.recorder.Ended()[beforeSpans:])
			if len(business) != 1 {
				t.Fatalf("business spans = %d, want 1", len(business))
			}
			assertSpan(t, business, "service_a.execute", rootTraceID, outcomeStatus(tc.a))
		})
	}
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	mws := []middleware.Middleware{
		tracing.Server(tracing.WithTracerProvider(provider)),
		recovery.Recovery(),
	}
	workerCalls := &atomic.Int32{}
	worker := &countingWorker{calls: workerCalls, delegate: NewWorkerService()}
	bHTTP := khttp.NewServer(khttp.Address("127.0.0.1:0"), khttp.Middleware(mws...))
	v1.RegisterWorkerHTTPServer(bHTTP, worker)
	bGRPC := kgrpc.NewServer(kgrpc.Address("127.0.0.1:0"), kgrpc.Middleware(mws...))
	v1.RegisterWorkerServer(bGRPC, worker)
	bHTTPEndpoint := startHTTPServer(t, bHTTP)
	bGRPCEndpoint := startGRPCServer(t, bGRPC)

	httpWorkerConn, err := khttp.NewClient(context.Background(),
		khttp.WithEndpoint(bHTTPEndpoint),
		khttp.WithMiddleware(tracing.Client(tracing.WithTracerProvider(provider))),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpWorkerConn.Close() })
	grpcWorkerConn, err := kgrpc.NewClient(context.Background(),
		kgrpc.WithEndpoint(bGRPCEndpoint),
		kgrpc.WithMiddleware(tracing.Client(tracing.WithTracerProvider(provider))),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = grpcWorkerConn.Close() })

	validation := NewValidationService(v1.NewWorkerHTTPClient(httpWorkerConn), v1.NewWorkerClient(grpcWorkerConn))
	aHTTP := khttp.NewServer(khttp.Address("127.0.0.1:0"), khttp.Middleware(mws...))
	v1.RegisterValidationHTTPServer(aHTTP, validation)
	aGRPC := kgrpc.NewServer(kgrpc.Address("127.0.0.1:0"), kgrpc.Middleware(mws...))
	v1.RegisterValidationServer(aGRPC, validation)
	aHTTPEndpoint := startHTTPServer(t, aHTTP)
	aGRPCEndpoint := startGRPCServer(t, aGRPC)

	httpConn, err := khttp.NewClient(context.Background(),
		khttp.WithEndpoint(aHTTPEndpoint),
		khttp.WithMiddleware(tracing.Client(tracing.WithTracerProvider(provider))),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpConn.Close() })
	grpcConn, err := kgrpc.NewClient(context.Background(),
		kgrpc.WithEndpoint(aGRPCEndpoint),
		kgrpc.WithMiddleware(tracing.Client(tracing.WithTracerProvider(provider))),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = grpcConn.Close() })

	return &testHarness{
		httpClient:  v1.NewValidationHTTPClient(httpConn),
		grpcClient:  v1.NewValidationClient(grpcConn),
		workerCalls: workerCalls,
		provider:    provider,
		recorder:    recorder,
	}
}

func startHTTPServer(t *testing.T, server *khttp.Server) string {
	t.Helper()
	endpoint, err := server.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Start(context.Background()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	})
	return endpoint.String()
}

func startGRPCServer(t *testing.T, server *kgrpc.Server) string {
	t.Helper()
	endpoint, err := server.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Start(context.Background()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	})
	return endpoint.Host
}

func businessSpans(spans []sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	result := make([]sdktrace.ReadOnlySpan, 0, 2)
	for _, span := range spans {
		if span.Name() == "service_a.execute" || span.Name() == "service_b.execute" {
			result = append(result, span)
		}
	}
	return result
}

func assertSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string, traceID trace.TraceID, status codes.Code) {
	t.Helper()
	for _, span := range spans {
		if span.Name() != name {
			continue
		}
		if span.SpanContext().TraceID() != traceID {
			t.Fatalf("%s trace ID = %s, want %s", name, span.SpanContext().TraceID(), traceID)
		}
		if span.Status().Code != status {
			t.Fatalf("%s status = %s, want %s", name, span.Status().Code, status)
		}
		return
	}
	t.Fatalf("span %q not found", name)
}

func outcomeStatus(outcome v1.Outcome) codes.Code {
	if outcome == v1.Outcome_OUTCOME_ERROR {
		return codes.Error
	}
	return codes.Ok
}
