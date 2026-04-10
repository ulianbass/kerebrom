.PHONY: build build-onnx test clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/kerebrom ./cmd/kerebrom
	@mkdir -p ~/.local/bin && cp bin/kerebrom ~/.local/bin/kerebrom

build-onnx:
	go build -tags onnx -ldflags="-s -w -X main.version=$(VERSION)" -o bin/kerebrom-onnx ./cmd/kerebrom

test:
	go test ./... -count=1 -race

clean:
	rm -rf bin/

run-dashboard: build
	./bin/kerebrom dashboard --port 8420

run-api: build
	./bin/kerebrom api --port 8080
