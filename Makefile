.PHONY: fmt fmt-check lint test build proto check e2e-test release-check

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
	# plbxd comes too: plbx autostarts it from beside itself, so a plbx built
	# without one cannot run a sandbox at all.
	go build -ldflags "-X main.version=$(VERSION)" -o plbx ./cmd/plbx
	go build -ldflags "-X main.version=$(VERSION)" -o plbxd ./cmd/plbxd

proto:     ## regenerate the daemon's wire contract from plbx.proto
	protoc \
	  --proto_path=internal/api/rpc/plbxv1 \
	  --go_out=internal/api/rpc/plbxv1 --go_opt=paths=source_relative \
	  --go-grpc_out=internal/api/rpc/plbxv1 --go-grpc_opt=paths=source_relative \
	  internal/api/rpc/plbxv1/plbx.proto

e2e-test:  ## end to end against a real runtime (needs docker/OrbStack)
	./scripts/e2e.sh

check: fmt-check lint test ## run every check

release-check: ## dry-run the release build locally (no publish)
	goreleaser release --snapshot --clean --skip=publish
