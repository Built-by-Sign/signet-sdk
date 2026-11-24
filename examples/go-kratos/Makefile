VERSION=$(shell git describe --tags --always)

.PHONY: build
# build
build:
	GOPRIVATE=github.com/built-by-sign/* mkdir -p bin/ && go build -ldflags "-X main.Version=$(VERSION)" -o ./bin/ ./...
