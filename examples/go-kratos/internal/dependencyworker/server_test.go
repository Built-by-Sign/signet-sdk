package dependencyworker

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHandlerReturnsExpectedStatusByMode(t *testing.T) {
    handler := NewHandler()
    cases := []struct {
        path string
        want int
    }{
        {path: "/dependency/success", want: http.StatusNoContent},
        {path: "/dependency/error", want: http.StatusInternalServerError},
        {path: "/dependency/unknown", want: http.StatusBadRequest},
    }
    for _, tc := range cases {
        request := httptest.NewRequest(http.MethodGet, tc.path, nil)
        response := httptest.NewRecorder()
        handler.ServeHTTP(response, request)
        if response.Code != tc.want {
            t.Fatalf("%s status = %d, want %d", tc.path, response.Code, tc.want)
        }
    }
}

func TestHandlerTimeoutReturnsWhenRequestContextIsCanceled(t *testing.T) {
    handler := NewHandler()
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    request := httptest.NewRequest(http.MethodGet, "/dependency/timeout", nil).WithContext(ctx)
    response := httptest.NewRecorder()

    handler.ServeHTTP(response, request)
    if response.Code != http.StatusGatewayTimeout {
        t.Fatalf("status = %d, want %d", response.Code, http.StatusGatewayTimeout)
    }
}