package main

import (
	"context"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"log"
	"log/slog"
	"os"
	"time"

	v1 "kratos-usage/api/trace/v1"
	"kratos-usage/internal/traceapp"

	signet "github.com/built-by-sign/signet/sdk"
	kratossignet "github.com/built-by-sign/signet/sdk/contrib/kratos"
	"github.com/go-kratos/kratos/v3"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func main() {
	ctx := context.Background()
	instanceID, _ := os.Hostname()
	token := os.Getenv("SIGNET_OTLP_API_KEY")
	if token == "" {
		log.Fatal("SIGNET_OTLP_API_KEY is required")
	}

	serverTLS, err := signet.TLSConfigFromFiles(os.Getenv("OTEL_EXPORTER_OTLP_CERTIFICATE"), "", "")
	if err != nil {
		log.Fatal(err)
	}
	core, err := signet.Init(signet.Config{
		ServiceName:    env("OTEL_SERVICE_NAME", "signet-go-validation"),
		ServiceVersion: env("OTEL_SERVICE_VERSION", "dev"),
		InstanceID:     instanceID,
		Environment:    env("OTEL_ENVIRONMENT", "local"),
		OTLPEndpoint:   env("OTEL_ENDPOINT", "grpcs://localhost:4317"),
		OTLPTLS:        serverTLS,
		OTLPHeaders:    map[string]string{"Authorization": "Bearer " + token},
		Metrics:        signet.MetricsConfig{Enabled: true, Views: []sdkmetric.View{kratossignet.HistogramMetricView()}},
		Tracing:        signet.TracingConfig{Enabled: true, BatchTimeout: 200 * time.Millisecond},
	})
	if err != nil {
		log.Fatal(err)
	}
	obs := kratossignet.Wrap(core)
	slog.SetDefault(slog.New(obs.Core().WithLogging(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
	}))))

	defer func() {
		if err := obs.Close(); err != nil {
			log.Printf("close observability: %v", err)
		}
	}()
	httpConn, err := khttp.NewClient(ctx,
		khttp.WithEndpoint(env("WORKER_HTTP_ENDPOINT", "http://127.0.0.1:8090")),
		khttp.WithTimeout(2*time.Second),
		khttp.WithMiddleware(obs.HTTPClientMiddleware()),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer httpConn.Close()

	grpcConn, err := kgrpc.NewClient(ctx,
		kgrpc.WithEndpoint(env("WORKER_GRPC_ENDPOINT", "127.0.0.1:9090")),
		kgrpc.WithTimeout(2*time.Second),
		kgrpc.WithOptions(obs.GRPCDialOptions()...),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer grpcConn.Close()

	service := traceapp.NewValidationService(v1.NewWorkerHTTPClient(httpConn), v1.NewWorkerClient(grpcConn))
	httpServer, grpcServer := traceapp.NewValidationServers(
		env("VALIDATION_HTTP_ADDR", ":8081"),
		env("VALIDATION_GRPC_ADDR", ":9081"),
		obs,
		service,
	)
	app := kratos.New(
		kratos.ID(instanceID),
		kratos.Name("signet-go-validation"),
		kratos.Logger(slog.Default()),
		kratos.Server(httpServer, grpcServer),
	)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
