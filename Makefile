BINARY := l7LoadBalancer
BIN_DIR := bin
PKG := ./...

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the binary to bin/
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

.PHONY: run
run: build ## Build and run against configs/example.yaml
	./$(BIN_DIR)/$(BINARY) -config configs/example.yaml

.PHONY: test
test: ## Run all tests
	go test $(PKG)

.PHONY: test-race
test-race: ## Run tests with race detector
	go test -race $(PKG)

.PHONY: bench
bench: ## Run Go benchmarks
	go test -bench=. -benchmem $(PKG)

.PHONY: fmt
fmt: ## Format code
	gofmt -w -s .
	@command -v goimports >/dev/null 2>&1 && goimports -w . || echo "goimports not installed; skipping"

.PHONY: tidy
tidy: ## Tidy modules
	go mod tidy

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
