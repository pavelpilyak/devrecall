.PHONY: build run test lint clean relay-deploy relay-test

BINARY := devrecall
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -tags "fts5" -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/devrecall

run: build
	./bin/$(BINARY)

test:
	go test -tags "fts5" ./... -race -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

relay-deploy:
	cd relay && npx wrangler deploy

relay-test:
	cd relay && npx vitest run
