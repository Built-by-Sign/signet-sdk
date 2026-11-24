# syntax=docker/dockerfile:1.7

FROM golang:1.27.3 AS builder

ARG SERVICE=otel-validation
WORKDIR /src/signet
COPY go.mod go.sum ./
WORKDIR /src/signet/examples/go-kratos
COPY examples/go-kratos/go.mod examples/go-kratos/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . /src/signet
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/${SERVICE}

FROM debian:stable-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/service /app/service
WORKDIR /app

EXPOSE 8081 9081 8090 9090
CMD ["/app/service"]
