.PHONY: build generate test test-unit test-integration test-e2e

VERSION ?= $(shell ./scripts/resolve-version.sh)
LDFLAGS ?= -s -w -X main.Version=$(VERSION)
# 传 BUILD_TAGS=embed 才会把 internal/web/dist 嵌进二进制；
# 默认留空，方便只编后端时不必先跑一遍前端构建。
BUILD_TAGS ?=

build:
	CGO_ENABLED=0 go build -tags="$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server

generate:
	go generate ./ent
	go generate ./cmd/server

test:
	go test ./...
	golangci-lint run ./...

test-unit:
	go test -tags=unit ./...

test-integration:
	go test -tags=integration ./...

test-e2e:
	./scripts/e2e-test.sh

test-e2e-local:
	go test -tags=e2e -v -timeout=300s ./internal/integration/...
