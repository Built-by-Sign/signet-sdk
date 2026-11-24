package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLiveHandler(t *testing.T) {
	h := New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.LiveHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %q", body["status"])
	}
}

func TestReadyHandler_AllHealthy(t *testing.T) {
	h := New()
	h.Register("db", func(context.Context) error { return nil })
	h.Register("cache", func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestReadyHandler_OneUnhealthy(t *testing.T) {
	h := New()
	h.Register("db", func(context.Context) error { return nil })
	h.Register("cache", func(context.Context) error { return errors.New("connection refused") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["cache"] != "connection refused" {
		t.Fatalf("expected cache error, got %q", body["cache"])
	}
	if body["db"] != "ok" {
		t.Fatalf("expected db ok, got %q", body["db"])
	}
}

func TestReadyHandler_Timeout(t *testing.T) {
	h := New(WithReadyTimeout(10 * time.Millisecond))
	h.Register("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["slow"] != context.DeadlineExceeded.Error() {
		t.Fatalf("slow = %q, want deadline exceeded", body["slow"])
	}
}

func TestReadyHandler_NoCheckers(t *testing.T) {
	h := New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with no checkers, got %d", rec.Code)
	}
}
