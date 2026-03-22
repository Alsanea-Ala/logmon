# Variables
AGENT_CMD       = ./cmd/agent
SERVER_CMD      = ./cmd/server
AGENT_BIN       = ./bin/agent
SERVER_BIN      = ./bin/server
GO              = go
GOFLAGS         = -v
MIN_GO_VERSION = 1.21


# ====================================Setup==============================================

.PHONY: setup
setup: check/go install/lint tidy setup/hooks
	@echo "Setup complete — ready to develop!"

check/go:
	@echo "Checking Go version..."
	@GO_VERSION=$$(go version | awk '{print $$3}' | sed 's/go//'); \
	REQUIRED="$(MIN_GO_VERSION)"; \
	if [ "$$(printf '%s\n' "$$REQUIRED" "$$GO_VERSION" | sort -V | head -n1)" = "$$REQUIRED" ]; then \
		echo "Go $$GO_VERSION ok"; \
	else \
		echo "Go $$GO_VERSION is below minimum required $$REQUIRED"; \
		exit 1; \
	fi

install/lint:
	@echo "Installing golangci-lint..."
	@if command -v golangci-lint > /dev/null 2>&1; then \
		echo "golangci-lint already installed — skipping"; \
	else \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
		if ! command -v golangci-lint > /dev/null 2>&1; then \
			echo "golangci-lint install failed — check your GOPATH/bin is in PATH"; \
			exit 1; \
		fi; \
		echo "golangci-lint installed"; \
	fi

tidy:
	@echo "Running go mod tidy..."
	@$(GO) mod tidy
	@echo "Dependencies tidied"

setup/hooks:
	@echo "Installing lefthook..."
	@go install github.com/evilmartians/lefthook@latest
	@echo "✅ lefthook installed"
	@echo "Installing git hooks..."
	@lefthook install
	@echo "✅ Git hooks installed"

# ====================================Run==============================================
.PHONY: run/agent run/server
run/agent:
	$(GO) run $(AGENT_CMD)/...

run/server:
	$(GO) run $(SERVER_CMD)/...

# ====================================Build==============================================
.PHONY: build build/agent build/server
build: build/agent build/server

build/agent:
	$(GO) build $(GOFLAGS) -o $(AGENT_BIN) $(AGENT_CMD)

build/server:
	$(GO) build $(GOFLAGS) -o $(SERVER_BIN) $(SERVER_CMD)

# ====================================Code Quality==========================================
.PHONY: fmt vet lint tidy
fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

# ==================================Clean================================================
.PHONY: clean clean/bin clean/conf clean/all

clean: clean/bin clean/conf

clean/bin:
	rm -rf ./bin

clean/conf:
# 	rm -f agent.yaml server.yaml *.log
	rm -f  *.log

# ====================================Helpers==========================================
.PHONY: help
help:
	@echo "Usage:"
	@grep -E '^[a-zA-Z_/]+:.*' makefile | awk -F ':' '{printf "  make %-20s\n", $$1}'