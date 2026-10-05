.PHONY: build test lint install clean

BIN_EXT := $(if $(filter windows,$(shell go env GOOS)),.exe,)

build:
	go build -trimpath -o bin/eu-tenders-pp-cli$(BIN_EXT) ./cmd/eu-tenders-pp-cli

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/eu-tenders-pp-cli

clean:
	rm -rf bin/

build-mcp:
	go build -trimpath -o bin/eu-tenders-pp-mcp$(BIN_EXT) ./cmd/eu-tenders-pp-mcp

install-mcp:
	go install ./cmd/eu-tenders-pp-mcp

build-all: build build-mcp
