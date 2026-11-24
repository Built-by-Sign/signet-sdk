package tracing

import (
	"context"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestNewTracerProvider_PlaintextBearerToken(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	receiver := &traceReceiver{headers: make(chan string, 1)}
	server := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(server, receiver)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	tp, err := NewTracerProvider(resource.Empty(), ProviderConfig{
		OTLPEndpoint: "grpc://" + listener.Addr().String(),
		OTLPHeaders:  map[string]string{"Authorization": "Bearer test-token"},
		SampleRatio:  nil,
		BatchTimeout: time.Second, MaxBatchSize: 512, MaxQueueSize: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	_, span := tp.Tracer("test").Start(context.Background(), "plain-bearer")
	span.End()
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case header := <-receiver.headers:
		if header != "Bearer test-token" {
			t.Fatalf("authorization header = %q", header)
		}
	case <-ctx.Done():
		t.Fatal("no OTLP traces authorization header received")
	}
}

type traceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer
	headers chan string
}

func (r *traceReceiver) Export(ctx context.Context, _ *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	select {
	case r.headers <- strings.Join(md.Get("authorization"), ","):
	default:
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func TestBuildSampler(t *testing.T) {
	tests := []struct {
		name string
		rate float64
		want sdktrace.SamplingDecision
	}{
		{name: "zero drops", rate: 0, want: sdktrace.Drop},
		{name: "one records", rate: 1, want: sdktrace.RecordAndSample},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSampler(tt.rate).ShouldSample(sdktrace.SamplingParameters{
				ParentContext: context.Background(),
			})
			if result.Decision != tt.want {
				t.Fatalf("decision = %v, want %v", result.Decision, tt.want)
			}
		})
	}
}

func TestProviderDefaultsAndSampling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Content-Type", "application/x-protobuf") }))
	defer server.Close()
	for _, tc := range []struct {
		name      string
		ratio     *float64
		queue     int
		wantRatio float64
		wantBatch int
	}{
		{"omitted", nil, 0, 1, 512},
		{"explicit zero", new(float64), 4, 0, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := ProviderConfig{OTLPEndpoint: server.URL, SampleRatio: tc.ratio, MaxQueueSize: tc.queue}
			provider, cfg, err := NewTracerProviderWithConfig(resource.Empty(), input)
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Shutdown(context.Background())
			if cfg.BatchTimeout != 5*time.Second || cfg.MaxBatchSize != tc.wantBatch || *cfg.SampleRatio != tc.wantRatio {
				t.Fatalf("resolved: %+v", cfg)
			}
			if tc.queue == 0 && cfg.MaxQueueSize != 2048 {
				t.Fatal(cfg.MaxQueueSize)
			}
			if input.BatchTimeout != 0 || input.MaxBatchSize != 0 || (tc.ratio != nil && cfg.SampleRatio == tc.ratio) {
				t.Fatal("caller configuration was mutated or aliased")
			}
			if tc.ratio != nil {
				*tc.ratio = 1
			}
			_, span := provider.Tracer("test").Start(context.Background(), "sampling")
			if span.IsRecording() != (tc.wantRatio == 1) {
				t.Fatal("resolved sampling not applied")
			}
			span.End()
		})
	}
}

func TestProviderRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  ProviderConfig
	}{
		{"negative timeout", ProviderConfig{BatchTimeout: -1}},
		{"negative batch", ProviderConfig{MaxBatchSize: -1}},
		{"negative queue", ProviderConfig{MaxQueueSize: -1}},
		{"batch exceeds queue", ProviderConfig{MaxBatchSize: 5, MaxQueueSize: 4}},
		{"negative ratio", ProviderConfig{SampleRatio: ratioPointer(-0.1)}},
		{"ratio above one", ProviderConfig{SampleRatio: ratioPointer(1.1)}},
		{"NaN", ProviderConfig{SampleRatio: ratioPointer(math.NaN())}},
		{"infinity", ProviderConfig{SampleRatio: ratioPointer(math.Inf(1))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.OTLPEndpoint = "http://localhost:4318"
			p, err := NewTracerProvider(resource.Empty(), tc.cfg)
			if err == nil {
				p.Shutdown(context.Background())
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func ratioPointer(v float64) *float64 { return &v }
