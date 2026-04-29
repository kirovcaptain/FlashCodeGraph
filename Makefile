.PHONY: build test lint clean setup

OS := $(shell uname -s 2>/dev/null || echo Windows)

setup: ## Check and guide dependency installation
	@echo "Checking build dependencies..."
	@command -v go >/dev/null 2>&1 || { \
		echo "✗ Go not found."; \
		case "$(OS)" in \
			Linux)  echo "  → sudo apt install golang-go  OR  https://go.dev/dl/" ;; \
			Darwin) echo "  → brew install go" ;; \
			*)      echo "  → https://go.dev/dl/" ;; \
		esac; exit 1; }
	@echo "✓ Go $$(go version | awk '{print $$3}')"
	@(command -v gcc >/dev/null 2>&1 || command -v cc >/dev/null 2>&1) || { \
		echo "✗ C compiler not found (required by CGO / tree-sitter)."; \
		case "$(OS)" in \
			Linux)  echo "  → sudo apt install build-essential" ;; \
			Darwin) echo "  → xcode-select --install" ;; \
			*)      echo "  → Install MinGW-w64: https://www.mingw-w64.org/" ;; \
		esac; exit 1; }
	@echo "✓ C compiler"
	@echo "All dependencies satisfied."

BINARY := fcg
BUILD_DIR := bin

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/fcg/

test:
	go test ./... -v -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR)
