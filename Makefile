GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo unversioned)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X nepse-api/src/shared/version.Version=$(VERSION) -X nepse-api/src/shared/version.Commit=$(COMMIT) -X nepse-api/src/shared/version.BuildMode=production

.PHONY: build build-rest build-mcp run-rest run-mcp test check fmt fmt-check vet tidy

build: build-rest build-mcp

build-rest:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o bin/nepse-rest ./src/cmd/rest

build-mcp:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o bin/nepse-mcp ./src/cmd/mcp

test:
	set -a; if [ -f .env ]; then . ./.env; fi; set +a; $(GO) test -v -race -count=1 -timeout 6m ./...

# Development commands.
run-rest:
	set -a; if [ -f .env ]; then . ./.env; fi; set +a; $(GO) run ./src/cmd/rest

run-mcp:
	set -a; if [ -f .env ]; then . ./.env; fi; set +a; $(GO) run ./src/cmd/mcp

fmt:
	gofmt -w $$(find src tests -name '*.go' -type f)

fmt-check:
	@test -z "$$(gofmt -l $$(find src tests -name '*.go' -type f))" || { gofmt -l $$(find src tests -name '*.go' -type f); exit 1; }

vet:
	$(GO) vet ./...

check: fmt-check vet build

tidy:
	$(GO) mod tidy
