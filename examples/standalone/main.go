package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	signet "github.com/built-by-sign/signet-sdk"
	signethttp "github.com/built-by-sign/signet-sdk/contrib/nethttp"

	"github.com/samber/lo"

	_ "go.uber.org/automaxprocs"
)

var (
	Name    = "standalone-demo"
	Version = "0.1.0"

	id, _ = os.Hostname()
)

func main() {
	if err := run(); err != nil {
		slog.Error("standalone stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	token := os.Getenv("SIGNET_OTLP_API_KEY")
	if token == "" {
		return fmt.Errorf("SIGNET_OTLP_API_KEY is required")
	}

	serverTLS, err := signet.TLSConfigFromFiles(os.Getenv("OTEL_EXPORTER_OTLP_CERTIFICATE"), "", "")
	if err != nil {
		return err
	}
	obs, err := signet.Init(signet.Config{
		ServiceName:    Name,
		ServiceVersion: Version,
		InstanceID:     id,
		Environment:    lo.CoalesceOrEmpty(os.Getenv("OTEL_ENVIRONMENT"), "staging"),
		OTLPEndpoint:   lo.CoalesceOrEmpty(os.Getenv("OTEL_ENDPOINT"), "grpcs://localhost:4317"),
		OTLPTLS:        serverTLS,
		OTLPHeaders:    map[string]string{"Authorization": "Bearer " + token},
		Metrics:        signet.MetricsConfig{Enabled: true, PushInterval: time.Second},
		Tracing:        signet.TracingConfig{Enabled: true, BatchTimeout: 500 * time.Millisecond},
	})
	if err != nil {
		return fmt.Errorf("initialize observability: %w", err)
	}

	slog.SetDefault(slog.New(obs.WithLogging(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
	}))))

	defer func() {
		if err := obs.Close(); err != nil {
			slog.Error("close observability", "err", err)
		}
	}()

	health := obs.HealthHandler()
	health.Register("self", func(context.Context) error { return nil })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service":   Name,
			"endpoints": []string{"GET /hello?name=world", "GET /work?delay_ms=250", "GET /error"},
		})
	})
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "world"
		}
		slog.InfoContext(r.Context(), "hello request", "name", name)
		writeJSON(w, http.StatusOK, map[string]string{"message": "hello, " + name})
	})
	mux.HandleFunc("GET /work", func(w http.ResponseWriter, r *http.Request) {
		delay := boundedDelay(r.URL.Query().Get("delay_ms"))
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": r.Context().Err().Error()})
		case <-timer.C:
			writeJSON(w, http.StatusOK, map[string]any{"worked_for_ms": delay.Milliseconds()})
		}
	})
	mux.HandleFunc("GET /error", func(w http.ResponseWriter, r *http.Request) {
		slog.ErrorContext(r.Context(), "example failure")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "example failure"})
	})
	mux.Handle("GET /healthz", health.LiveHandler())
	mux.Handle("GET /readyz", health.ReadyHandler())

	// Creates a server span and standard HTTP metrics for every request.
	handler := signethttp.NewHandler(mux, "standalone.http")
	srv := &http.Server{Addr: lo.CoalesceOrEmpty(os.Getenv("LISTEN"), ":8000"), Handler: handler}
	defer srv.Close()

	quit, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)

	go func() {
		slog.Info("listening at", "addr", srv.Addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("listen on %s: %w", srv.Addr, err)
		}
		return nil
	case <-quit.Done():
	}

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	return nil
}

func boundedDelay(raw string) time.Duration {
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		ms = 250
	}
	if ms > 2_000 {
		ms = 2_000
	}
	return time.Duration(ms) * time.Millisecond
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", "err", err)
	}
}
