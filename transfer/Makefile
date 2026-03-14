BIN     := transfer
MODULE  := $(shell go env GOMODULE 2>/dev/null || head -1 go.mod | awk '{print $$2}')
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

# Static binaries — no CGO, no libc dependency.
.PHONY: build build-linux build-windows test clean

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BIN) .

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)-linux-amd64 .

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)-windows-amd64.exe .

# Build all three platforms in one shot.
all: build build-linux build-windows

test:
	go test -v -count=1 ./...

clean:
	rm -f $(BIN) $(BIN)-linux-amd64 $(BIN)-windows-amd64.exe
