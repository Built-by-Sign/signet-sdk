# Signet Go-Kratos trace example

[Project overview](../../README.md) · [Compose installation](../../docs/install-compose.md) · [SDK integration](../../docs/integrate-sdk.md)

This example contains two Kratos microservices. Both expose HTTP and gRPC and
both use the Signet SDK from this repository through the `go.mod` replace rule.

- `otel-validation` is service A (`:8081` HTTP, `:9081` gRPC).
- `otel-worker` is service B (`:8090` HTTP, `:9090` gRPC).
- An HTTP request to A calls B over HTTP; a gRPC request to A calls B over gRPC.
- If A fails, the request short-circuits and B is not called.

## Run

Use Go 1.27.3 or newer and start the [Compose platform](../../docs/install-compose.md) first.
This example is a separate Go module. In **each terminal**, start from the repository
root and enter its directory:

```bash
cd examples/go-kratos
export OTEL_EXPORTER_OTLP_CERTIFICATE="$(pwd)/../../compose/secrets/app/server-ca.crt"
export SIGNET_OTLP_API_KEY="$(cat ../../compose/secrets/app/token)"
```

Start B in the first terminal:

```bash
OTEL_ENDPOINT=grpcs://localhost:4317 go run ./cmd/otel-worker
```

Start A in the second terminal:

```bash
OTEL_ENDPOINT=grpcs://localhost:4317 go run ./cmd/otel-validation
```

By default, A calls B at `http://127.0.0.1:8090` and `127.0.0.1:9090`.
Override `WORKER_HTTP_ENDPOINT` and `WORKER_GRPC_ENDPOINT` when B runs elsewhere.
The default service names are `signet-go-validation` and `signet-go-worker`.
Both send metrics and traces to OTLP; host-process stdout is not collected by the
platform's Docker filelog receiver. Use the [SDK telemetry test stack](../../tests/README.md)
for automated trace, metric, and log verification.

## HTTP scenarios

```bash
# (A OK, B OK)
curl -i -X POST http://localhost:8081/v1/trace/chain \
  -H 'Content-Type: application/json' \
  -d '{"a":1,"b":1}'

# (A OK, B ERROR)
curl -i -X POST http://localhost:8081/v1/trace/chain \
  -H 'Content-Type: application/json' \
  -d '{"a":1,"b":2}'

# (A ERROR, B is not called)
curl -i -X POST http://localhost:8081/v1/trace/chain \
  -H 'Content-Type: application/json' \
  -d '{"a":2}'

# A only
curl -i -X POST http://localhost:8081/v1/trace/local \
  -H 'Content-Type: application/json' \
  -d '{"a":1}'
curl -i -X POST http://localhost:8081/v1/trace/local \
  -H 'Content-Type: application/json' \
  -d '{"a":2}'
```

## gRPC scenarios

Install `grpcurl` to run these requests. Server reflection is enabled by the Kratos
gRPC server. The same inputs are available through `trace.v1.Validation`:

```bash
grpcurl -plaintext -d '{"a":1,"b":1}' \
  localhost:9081 trace.v1.Validation/RunChain
grpcurl -plaintext -d '{"a":1,"b":2}' \
  localhost:9081 trace.v1.Validation/RunChain
grpcurl -plaintext -d '{"a":2}' \
  localhost:9081 trace.v1.Validation/RunChain

grpcurl -plaintext -d '{"a":1}' \
  localhost:9081 trace.v1.Validation/RunLocal
grpcurl -plaintext -d '{"a":2}' \
  localhost:9081 trace.v1.Validation/RunLocal
```

## Verify

From `examples/go-kratos`:

```bash
go test ./... -count=1
go build ./cmd/otel-validation ./cmd/otel-worker
```

The integration tests assert HTTP and gRPC propagation, business span status,
a shared trace ID across A and B, and zero B calls when A fails.

These examples always use TLS + Bearer authentication. Set `SIGNET_OTLP_API_KEY`
to the raw API key and `OTEL_EXPORTER_OTLP_CERTIFICATE` to a CA file path.
Startup code constructs `OTLPHeaders["Authorization"] = "Bearer " + token`.
The CA file is required. Go initialization uses `signet.Init(signet.Config{...})`
with explicit service, transport, authentication, metrics and tracing fields.
