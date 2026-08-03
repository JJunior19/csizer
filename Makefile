GO ?= go
GOLANGCI_LINT ?= golangci-lint
BINARY ?= bin/csizer

.PHONY: build check fmt fmt-check lint test test-race vet

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/csizer

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { printf 'Files need gofmt:\n%s\n' "$$(gofmt -l .)"; exit 1; }

lint:
	$(GOLANGCI_LINT) run

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: fmt-check vet lint test-race build
