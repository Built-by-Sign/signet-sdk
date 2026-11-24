package dependencyworker

import (
    "log/slog"
    "net/http"
    "strings"

    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func NewHandler() http.Handler {
    return otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        mode := strings.TrimPrefix(r.URL.Path, "/dependency/")
        slog.InfoContext(r.Context(), "dependency received",
            "event.name", "dependency.received",
            "validation.mode", mode,
        )
        switch mode {
        case "success":
            w.WriteHeader(http.StatusNoContent)
        case "error":
            http.Error(w, "controlled dependency worker error", http.StatusInternalServerError)
        case "timeout":
            <-r.Context().Done()
            http.Error(w, "dependency worker timeout", http.StatusGatewayTimeout)
        default:
            http.Error(w, "unsupported validation mode", http.StatusBadRequest)
        }
    }), "dependency.worker")
}
