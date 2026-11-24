package metrics

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestNewMeterProvider_WithEndpoint(t *testing.T) {
	t.Run("endpoint only", func(t *testing.T) { testMeterProviderEndpoint(t, false) })
	t.Run("endpoint and explicit reader", func(t *testing.T) { testMeterProviderEndpoint(t, true) })
}

func testMeterProviderEndpoint(t *testing.T, withReader bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	receiver := &metricReceiver{
		requests: make(chan *collectormetric.ExportMetricsServiceRequest, 1),
		headers:  make(chan string, 1),
	}
	collectormetric.RegisterMetricsServiceServer(server, receiver)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	res := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("test"))
	reader := sdkmetric.NewManualReader()
	cfg := ProviderConfig{
		OTLPEndpoint: "grpc://" + listener.Addr().String(),
		OTLPHeaders:  map[string]string{"Authorization": "Bearer test-token"},
		PushInterval: time.Hour,
	}
	if withReader {
		cfg.Readers = []sdkmetric.Reader{reader}
	}
	mp, err := NewMeterProvider(res, cfg, sdkmetric.WithView(sdkmetric.NewView(
		sdkmetric.Instrument{Name: "requests"}, sdkmetric.Stream{Name: "renamed_requests"})))
	if err != nil {
		t.Fatalf("NewMeterProvider with endpoint failed: %v", err)
	}
	cfg.OTLPHeaders["Authorization"] = "changed-after-construction"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mp.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	counter, err := mp.Meter("test").Int64Counter("requests")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 5)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mp.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-receiver.requests:
		found := false
		for _, rm := range request.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, metric := range sm.Metrics {
					if metric.Name == "renamed_requests" {
						points := metric.GetSum().GetDataPoints()
						if len(points) != 1 || points[0].GetAsInt() != 5 {
							t.Fatalf("unexpected exported counter: %v", metric)
						}
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatal("OTLP exporter did not send the counter transformed by the view")
		}
	case <-ctx.Done():
		t.Fatal("no OTLP metrics received")
	}
	select {
	case header := <-receiver.headers:
		if header != "Bearer test-token" {
			t.Fatalf("authorization header = %q", header)
		}
	case <-ctx.Done():
		t.Fatal("no OTLP metrics authorization header received")
	}
	if !withReader {
		return
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.ScopeMetrics) != 1 || len(data.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("explicit reader collected unexpected data: %v", data)
	}
	metric := data.ScopeMetrics[0].Metrics[0]
	sum, ok := metric.Data.(metricdata.Sum[int64])
	if metric.Name != "renamed_requests" || !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 5 {
		t.Fatalf("explicit reader collected unexpected counter: %v", metric)
	}
}

type metricReceiver struct {
	collectormetric.UnimplementedMetricsServiceServer
	requests chan *collectormetric.ExportMetricsServiceRequest
	headers  chan string
}

func (r *metricReceiver) Export(ctx context.Context, request *collectormetric.ExportMetricsServiceRequest) (*collectormetric.ExportMetricsServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	select {
	case r.headers <- strings.Join(md.Get("authorization"), ","):
	default:
	}
	select {
	case r.requests <- request:
	default:
	}
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}

func TestNewMeterProvider_RequiresExplicitReaderOrEndpoint(t *testing.T) {
	var typedNil *sdkmetric.ManualReader
	for _, tc := range []struct {
		name    string
		cfg     ProviderConfig
		opts    []sdkmetric.Option
		message string
	}{
		{"empty", ProviderConfig{}, nil, "OTLPEndpoint or Readers"},
		{"view only", ProviderConfig{}, []sdkmetric.Option{sdkmetric.WithView()}, "OTLPEndpoint or Readers"},
		{"reader option only", ProviderConfig{}, []sdkmetric.Option{sdkmetric.WithReader(sdkmetric.NewManualReader())}, "OTLPEndpoint or Readers"},
		{"nil reader", ProviderConfig{Readers: []sdkmetric.Reader{nil}}, nil, "Readers[0] must not be nil"},
		{"typed nil reader", ProviderConfig{Readers: []sdkmetric.Reader{typedNil}}, nil, "Readers[0] must not be nil"},
		{"nil reader with endpoint", ProviderConfig{OTLPEndpoint: "grpcs://localhost:4317", PushInterval: time.Second, Readers: []sdkmetric.Reader{nil}}, nil, "Readers[0] must not be nil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp, err := NewMeterProvider(resource.Empty(), tc.cfg, tc.opts...)
			if mp != nil {
				_ = mp.Shutdown(context.Background())
				t.Fatal("provider created for invalid configuration")
			}
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
}

func TestProviderDefaultInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, 250 * time.Millisecond} {
		reader := sdkmetric.NewManualReader()
		input := ProviderConfig{PushInterval: interval, Readers: []sdkmetric.Reader{reader}}
		p, cfg, err := NewMeterProviderWithConfig(resource.Empty(), input)
		if err != nil {
			t.Fatal(err)
		}
		want := interval
		if want == 0 {
			want = 15 * time.Second
		}
		if cfg.PushInterval != want || input.PushInterval != interval {
			t.Fatalf("resolved=%v input=%v", cfg.PushInterval, input.PushInterval)
		}
		counter, _ := p.Meter("test").Int64Counter("requests")
		counter.Add(context.Background(), 1)
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data.ScopeMetrics) == 0 {
			t.Fatal("reader not registered")
		}
		if err := p.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"", "http://localhost:4318"} {
		p, err := NewMeterProvider(resource.Empty(), ProviderConfig{OTLPEndpoint: endpoint, PushInterval: -time.Second, Readers: []sdkmetric.Reader{sdkmetric.NewManualReader()}})
		if err == nil {
			p.Shutdown(context.Background())
			t.Fatal("negative interval accepted")
		}
	}
}
