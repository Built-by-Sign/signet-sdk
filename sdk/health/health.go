package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Checker is a function that reports health status.
// Returns nil if healthy, error otherwise. Checkers must honor ctx so a
// hanging dependency (e.g. a DB ping) cannot block /readyz forever; the
// handler passes the request context, which is cancelled when the probe
// times out or disconnects.
type Checker func(ctx context.Context) error

const defaultReadyTimeout = 5 * time.Second

// Option configures a health Handler.
type Option func(*Handler)

// WithReadyTimeout bounds the total time spent running readiness checkers.
// A non-positive duration disables the handler-level timeout.
func WithReadyTimeout(timeout time.Duration) Option {
	return func(h *Handler) { h.readyTimeout = timeout }
}

// Handler provides /healthz and /readyz HTTP handlers.
type Handler struct {
	mu           sync.RWMutex
	checkers     map[string]Checker
	readyTimeout time.Duration
}

// New creates a health Handler.
func New(opts ...Option) *Handler {
	h := &Handler{
		checkers:     make(map[string]Checker),
		readyTimeout: defaultReadyTimeout,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register adds a named health checker.
func (h *Handler) Register(name string, checker Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers[name] = checker
}

// LiveHandler returns an HTTP handler for liveness checks (/healthz).
// Always returns 200 if the process is running.
func (h *Handler) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

// ReadyHandler returns an HTTP handler for readiness checks (/readyz).
// Runs all registered checkers; returns 503 if any fails.
func (h *Handler) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if h.readyTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, h.readyTimeout)
			defer cancel()
		}

		h.mu.RLock()
		checkers := make(map[string]Checker, len(h.checkers))
		for k, v := range h.checkers {
			checkers[k] = v
		}
		h.mu.RUnlock()

		result := make(map[string]string, len(checkers))
		allOk := true
		for name, check := range checkers {
			if err := check(ctx); err != nil {
				result[name] = err.Error()
				allOk = false
			} else {
				result[name] = "ok"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if allOk {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(result)
	})
}
