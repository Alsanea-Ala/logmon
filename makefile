.DEFAULT_GOAL := help

GO ?= go

.PHONY: run/agent run/server build build/agent build/server fmt vet lint test tidy clean help

run/agent:
	$(GO) run ./cmd/agent

run/server:
	$(GO) run ./cmd/server

build: build/agent build/server

build/agent:
	@mkdir -p bin
	$(GO) build -o ./bin/agent ./cmd/agent

build/server:
	@mkdir -p bin
	$(GO) build -o ./bin/server ./cmd/server

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf ./bin

help:
	@echo "make run/agent   Run the agent"
	@echo "make run/server  Run the server"
	@echo "make build       Build both binaries"
	@echo "make fmt         Format Go packages"
	@echo "make vet         Run go vet"
	@echo "make lint        Run golangci-lint"
	@echo "make test        Run all tests"
