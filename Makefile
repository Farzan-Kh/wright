# Wright — build automation.

BINARY  := wright
PKG     := github.com/farzan-kh/wright
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION)

.PHONY: build test lint tidy smoke clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/wright

test:
	go test ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

# Convenience wrapper: `make smoke REPO=you/scratch-repo`.
# Requires a config and the appropriate token env var to be set.
smoke: build
	./$(BINARY) smoke --config wright.yaml --repo $(REPO)

clean:
	rm -f $(BINARY)
	rm -rf dist
