package server

import (
	v1 "kratos-usage/api/helloworld/v1"
	"kratos-usage/internal/conf"
	"kratos-usage/internal/service"

	kratossignet "github.com/built-by-sign/signet-sdk/contrib/kratos"

	"log/slog"

	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/http"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, greeter *service.GreeterService, obs *kratossignet.Observability, logger *slog.Logger) *http.Server {
	// Recover business panics before metrics and tracing finish the request.
	mws := append(obs.Middlewares(), recovery.Recovery())

	var opts = []http.ServerOption{
		http.Middleware(mws...),
	}
	if c.Http.Network != "" {
		opts = append(opts, http.Network(c.Http.Network))
	}
	if c.Http.Addr != "" {
		opts = append(opts, http.Address(c.Http.Addr))
	}
	if c.Http.Timeout != nil {
		opts = append(opts, http.Timeout(c.Http.Timeout.AsDuration()))
	}
	srv := http.NewServer(opts...)
	v1.RegisterGreeterHTTPServer(srv, greeter)

	// Observability endpoints (/healthz, /readyz)
	obs.RegisterRoutes(srv)

	return srv
}
