VERSION ?= $(shell git describe --tags --always)
IMAGE_REPOSITORY ?= signet-standalone
IMAGE_TAG ?= latest
IMAGE ?= $(IMAGE_REPOSITORY):$(IMAGE_TAG)

.PHONY: build image push

build:
	mkdir -p bin/ && GOPRIVATE=github.com/built-by-sign/* go build -ldflags "-X main.Version=$(VERSION)" -o ./bin/ ./...

image:
	DOCKER_BUILDKIT=1 docker build \
		--file Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--tag $(IMAGE) \
		../..

push: image
	docker push $(IMAGE)
