# Standalone example

SDK example source for development and tests; it is not started with the platform.

[Compose installation](https://github.com/Built-by-Sign/Signet/blob/main/docs/install-compose.md)

## Run as a host process

Use Go 1.27.3 or newer and start an OTLP receiver that accepts TLS and Bearer
authentication. Set `OTEL_EXPORTER_OTLP_CERTIFICATE` to the absolute path to
the receiver's CA file and `SIGNET_OTLP_API_KEY` to the raw key. This example
has its own `go.mod`, so enter its directory before running it:

```bash
# From the repository root
cd examples/standalone
OTEL_ENDPOINT="${OTEL_ENDPOINT:-grpcs://localhost:4317}" \
  OTEL_ENVIRONMENT=compose LISTEN=127.0.0.1:18080 go run .
```

The default service name is `standalone-demo`, and the instance ID is the hostname.
Without `LISTEN`, the example binds to `:8000`; the command above uses port 18080
to avoid an existing service on 8000. Change the port if needed.

Metrics and traces are sent to the configured OTLP receiver. JSON logs go to
stdout.

## Generate traffic and verify

Wait for the example to become ready, then generate requests:

```bash
curl -fsS http://127.0.0.1:18080/healthz
curl -fsS http://127.0.0.1:18080/readyz
for i in $(seq 1 10); do
  curl -sS 'http://127.0.0.1:18080/hello?name=Signet'
  curl -sS 'http://127.0.0.1:18080/work?delay_ms=250'
  curl -sS 'http://127.0.0.1:18080/error'
  sleep 1
done
```

`/hello` and `/work` return HTTP 200; `/error` intentionally returns HTTP 500 and
logs `example failure`. `/work` caps its delay at 2 seconds, so it cannot by itself
exercise the default 3-second slow-trace retention rule.

Wait 30–60 seconds, then follow [platform verification](https://github.com/Built-by-Sign/Signet/blob/main/docs/install-compose.md#verification).
Select namespace=`compose` and service=`standalone-demo` in Grafana. Check
heartbeat and request metrics, error traces, and the trace IDs in local logs.
Ordinary traces are retained at 1%; error traces are the reliable verification case.

Stop the example with Ctrl+C.

These examples always use TLS + Bearer authentication. Set `SIGNET_OTLP_API_KEY`
to the raw API key and `OTEL_EXPORTER_OTLP_CERTIFICATE` to a CA file path.
Startup code constructs `OTLPHeaders["Authorization"] = "Bearer " + token`.
The CA file is required. Go initialization uses `signet.Init(signet.Config{...})`
with explicit service, transport, authentication, metrics and tracing fields.
