package traceapp

import (
	"context"
	"net"
	"testing"
	"time"

	v1 "kratos-usage/api/trace/v1"

	signet "github.com/built-by-sign/signet/sdk"
	kratossignet "github.com/built-by-sign/signet/sdk/contrib/kratos"
	otelmetrics "github.com/go-kratos/kratos/contrib/otel/v3/metrics"
	"github.com/go-kratos/kratos/v3/errors"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type panicWorker struct{ v1.UnimplementedWorkerServer }

func (*panicWorker) Execute(context.Context, *v1.ExecuteRequest) (*v1.ExecuteReply, error) {
	panic("test business panic")
}

// Exercise the actual serverMiddleware over both transports, with the SDK's
// real providers. The local OTLP sink also lets provider shutdown flush normally.
func TestServerMiddlewareRecordsPanic(t *testing.T) {
	for _, protocol := range []string{"http", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			sink := grpc.NewServer()
			collectortrace.RegisterTraceServiceServer(sink, &panicTestTraceSink{})
			collectormetric.RegisterMetricsServiceServer(sink, &panicTestMetricSink{})
			go func() { _ = sink.Serve(listener) }()
			t.Cleanup(sink.Stop)

			previousTracer := otel.GetTracerProvider()
			previousMeter := otel.GetMeterProvider()
			previousPropagator := otel.GetTextMapPropagator()
			t.Cleanup(func() {
				otel.SetTracerProvider(previousTracer)
				otel.SetMeterProvider(previousMeter)
				otel.SetTextMapPropagator(previousPropagator)
			})
			reader := sdkmetric.NewManualReader()
			core, err := signet.Init(signet.Config{
				ServiceName:  "panic-test",
				OTLPEndpoint: "grpc://" + listener.Addr().String(),
				Metrics:      signet.MetricsConfig{Enabled: true, Views: []sdkmetric.View{kratossignet.HistogramMetricView()}, Readers: []sdkmetric.Reader{reader}},
				Tracing:      signet.TracingConfig{Enabled: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			obs := kratossignet.Wrap(core)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := obs.CloseContext(ctx); err != nil {
					t.Error(err)
				}
			})
			recorder := tracetest.NewSpanRecorder()
			obs.Core().TracerProvider().RegisterSpanProcessor(recorder)

			var call func(context.Context, *v1.ExecuteRequest) (*v1.ExecuteReply, error)
			if protocol == "http" {
				server := khttp.NewServer(khttp.Address("127.0.0.1:0"), khttp.Middleware(serverMiddleware(obs)...))
				v1.RegisterWorkerHTTPServer(server, &panicWorker{})
				client, err := khttp.NewClient(context.Background(), khttp.WithEndpoint(startHTTPServer(t, server)))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				call = func(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteReply, error) {
					return v1.NewWorkerHTTPClient(client).Execute(ctx, req)
				}
			} else {
				server := kgrpc.NewServer(kgrpc.Address("127.0.0.1:0"), kgrpc.Middleware(serverMiddleware(obs)...))
				v1.RegisterWorkerServer(server, &panicWorker{})
				client, err := kgrpc.NewClient(context.Background(), kgrpc.WithEndpoint(startGRPCServer(t, server)))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				call = func(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteReply, error) {
					return v1.NewWorkerClient(client).Execute(ctx, req)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = call(ctx, &v1.ExecuteRequest{})
			if err == nil {
				t.Fatal("panic request succeeded")
			}
			if protocol == "http" && errors.Code(err) != 500 {
				t.Errorf("HTTP error = %v, want status 500", err)
			}
			if protocol == "grpc" && status.Code(err) != grpccodes.Internal {
				t.Errorf("gRPC error = %v, want Internal", err)
			}

			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("ended spans = %d, want one server span", len(spans))
			}
			if spans[0].SpanKind() != trace.SpanKindServer || spans[0].Status().Code != codes.Error {
				t.Errorf("server span kind/status = %v/%v, want Server/Error", spans[0].SpanKind(), spans[0].Status().Code)
			}

			var data metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &data); err != nil {
				t.Fatal(err)
			}
			var requests int64
			var durations uint64
			for _, scope := range data.ScopeMetrics {
				for _, metric := range scope.Metrics {
					switch metric.Name {
					case otelmetrics.DefaultServerRequestsCounterName:
						for _, point := range metric.Data.(metricdata.Sum[int64]).DataPoints {
							requests += point.Value
							code, _ := point.Attributes.Value("code")
							kind, _ := point.Attributes.Value("kind")
							operation, _ := point.Attributes.Value("operation")
							if code.AsInt64() != 500 || kind.AsString() != protocol || operation.AsString() != v1.OperationWorkerExecute {
								t.Errorf("unexpected request attributes: %v", point.Attributes)
							}
						}
					case otelmetrics.DefaultServerSecondsHistogramName:
						for _, point := range metric.Data.(metricdata.Histogram[float64]).DataPoints {
							durations += point.Count
						}
					}
				}
			}
			if requests != 1 || durations != 1 {
				t.Errorf("requests/duration observations = %d/%d, want 1/1", requests, durations)
			}
		})
	}
}

type panicTestTraceSink struct {
	collectortrace.UnimplementedTraceServiceServer
}

func (*panicTestTraceSink) Export(context.Context, *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type panicTestMetricSink struct {
	collectormetric.UnimplementedMetricsServiceServer
}

func (*panicTestMetricSink) Export(context.Context, *collectormetric.ExportMetricsServiceRequest) (*collectormetric.ExportMetricsServiceResponse, error) {
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}
