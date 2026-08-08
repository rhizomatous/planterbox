.PHONY: fmt fmt-check lint test build proto check release-check

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

fmt:       ## apply formatters (gofumpt & goimports)
	golangci-lint fmt

fmt-check: ## verify formatting without writing
	golangci-lint fmt --diff

lint:      ## static analysis (staticcheck, govet, errcheck, etc.)
	golangci-lint run

test:      ## unit tests
	go test ./...

build:     ## build both binaries
	# jardd comes too: jard autostarts it from beside itself, so a jard built
	# without one cannot run a sandbox at all.
	go build -ldflags "-X main.version=$(VERSION)" -o jard ./cmd/jard
	go build -ldflags "-X main.version=$(VERSION)" -o jardd ./cmd/jardd

proto:     ## regenerate the daemon's wire contract from jard.proto
	protoc \
	  --proto_path=internal/api/rpc/jardv1 \
	  --go_out=internal/api/rpc/jardv1 --go_opt=paths=source_relative \
	  --go-grpc_out=internal/api/rpc/jardv1 --go-grpc_opt=paths=source_relative \
	  internal/api/rpc/jardv1/jard.proto

check: fmt-check lint test ## run every check

release-check: ## dry-run the release build locally (no publish)
	goreleaser release --snapshot --clean --skip=publish
