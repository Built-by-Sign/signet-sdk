package server

import (
	v1 "kratos-usage/api/helloworld/v1"
	"kratos-usage/internal/conf"
	"kratos-usage/internal/service"

	kratossignet "github.com/built-by-sign/signet-sdk/contrib/kratos"

	"log/slog"

	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer new a gRPC server.
func NewGRPCServer(c *conf.Server, greeter *service.GreeterService, obs *kratossignet.Observability, logger *slog.Logger) *grpc.Server {
	// Recover business panics before metrics and tracing finish the request.
	mws := append(obs.Middlewares(), recovery.Recovery())

	var opts = []grpc.ServerOption{
		grpc.Middleware(mws...),
	}
	if c.Grpc.Network != "" {
		opts = append(opts, grpc.Network(c.Grpc.Network))
	}
	if c.Grpc.Addr != "" {
		opts = append(opts, grpc.Address(c.Grpc.Addr))
	}
	if c.Grpc.Timeout != nil {
		opts = append(opts, grpc.Timeout(c.Grpc.Timeout.AsDuration()))
	}
	srv := grpc.NewServer(opts...)
	v1.RegisterGreeterServer(srv, greeter)
	return srv
}
