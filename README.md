# Signet SDK

Signet SDK adds OpenTelemetry metrics, distributed traces, and trace-correlated
`slog` logs to Go services. It supports standard-library HTTP servers and
Go-Kratos applications.

## Quick start

- [Standard-library HTTP example](examples/standalone/README.md) shows how to
  instrument HTTP requests, expose health checks, and correlate application logs
  with traces.
- [Go-Kratos example](examples/go-kratos/README.md) runs two services and shows
  how to propagate traces across HTTP and gRPC calls.

Both examples use the SDK from this checkout. Their guides cover running against
an OTLP receiver with TLS and a Bearer API key.

## Using the SDK

Import `github.com/built-by-sign/signet-sdk` as `signet`. Call
[`signet.Init`](signet.go) at startup with a service name and enable metrics,
tracing, or both. Set an OTLP HTTP or gRPC endpoint to export telemetry. Enabling
metrics also registers Go runtime metrics and a service heartbeat.

Use `WithLogging` to add service identity and trace context to a `slog.Handler`.
Use the [HTTP adapter](contrib/nethttp/handler.go) or
[Kratos adapter](contrib/kratos/signet.go) to instrument requests, and
[`observable`](observable/decorator.go) to instrument individual operations.
Call `Close` during shutdown to flush pending telemetry.

## Signet platform

The [Signet deployment guide](https://github.com/Built-by-Sign/Signet/blob/main/docs/install-compose.md)
covers installing the Docker Compose platform, configuring its OTLP receiver,
and viewing telemetry in Grafana.

## License

[MIT](LICENSE)
