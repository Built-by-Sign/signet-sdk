# syntax=docker/dockerfile:1.7

FROM golang:1.27.3-alpine AS builder

WORKDIR /src/signet
COPY go.mod go.sum ./
COPY sdk ./sdk

WORKDIR /src/signet/examples/standalone
COPY examples/standalone/go.mod examples/standalone/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY examples/standalone ./

ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /out/standalone .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && adduser -D -H -u 10001 app
COPY --from=builder /out/standalone /usr/local/bin/standalone
USER app
EXPOSE 8000
ENTRYPOINT ["standalone"]
