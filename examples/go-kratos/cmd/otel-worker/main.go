package main

import (
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"log"
	"log/slog"
	"os"
	"time"

	"kratos-usage/internal/traceapp"

	signet "github.com/built-by-sign/signet/sdk"
	kratossignet "github.com/built-by-sign/signet/sdk/contrib/kratos"
	"github.com/go-kratos/kratos/v3"
)

func main() {
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
		ServiceName:    env("OTEL_SERVICE_NAME", "signet-go-worker"),
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
	service := traceapp.NewWorkerService()
	httpServer, grpcServer := traceapp.NewWorkerServers(
		env("WORKER_HTTP_ADDR", ":8090"),
		env("WORKER_GRPC_ADDR", ":9090"),
		obs,
		service,
	)
	app := kratos.New(
		kratos.ID(instanceID),
		kratos.Name("signet-go-worker"),
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
