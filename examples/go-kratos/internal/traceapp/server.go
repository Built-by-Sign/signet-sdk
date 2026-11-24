package traceapp

import (
	v1 "kratos-usage/api/trace/v1"

	kratossignet "github.com/built-by-sign/signet/sdk/contrib/kratos"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func NewValidationServers(httpAddr, grpcAddr string, obs *kratossignet.Observability, service *ValidationService) (*khttp.Server, *kgrpc.Server) {
	httpServer := khttp.NewServer(khttp.Address(httpAddr), khttp.Middleware(serverMiddleware(obs)...))
	v1.RegisterValidationHTTPServer(httpServer, service)
	obs.RegisterRoutes(httpServer)

	grpcServer := kgrpc.NewServer(kgrpc.Address(grpcAddr), kgrpc.Middleware(serverMiddleware(obs)...))
	v1.RegisterValidationServer(grpcServer, service)
	return httpServer, grpcServer
}

func NewWorkerServers(httpAddr, grpcAddr string, obs *kratossignet.Observability, service *WorkerService) (*khttp.Server, *kgrpc.Server) {
	httpServer := khttp.NewServer(khttp.Address(httpAddr), khttp.Middleware(serverMiddleware(obs)...))
	v1.RegisterWorkerHTTPServer(httpServer, service)
	obs.RegisterRoutes(httpServer)

	grpcServer := kgrpc.NewServer(kgrpc.Address(grpcAddr), kgrpc.Middleware(serverMiddleware(obs)...))
	v1.RegisterWorkerServer(grpcServer, service)
	return httpServer, grpcServer
}

func serverMiddleware(obs *kratossignet.Observability) []middleware.Middleware {
	// Recover business panics before metrics and tracing finish the request.
	return append(obs.Middlewares(), recovery.Recovery())
}
