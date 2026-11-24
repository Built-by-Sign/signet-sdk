VERSION ?= $(shell git describe --tags --always)

.PHONY: build

build:
	mkdir -p bin/ && GOPRIVATE=github.com/built-by-sign/* go build -ldflags "-X main.Version=$(VERSION)" -o ./bin/ ./...
