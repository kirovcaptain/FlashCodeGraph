.PHONY: build test lint clean

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
