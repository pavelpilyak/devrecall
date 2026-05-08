.PHONY: build run test lint clean relay-deploy relay-test desktop desktop-cli desktop-bundle desktop-test

BINARY := devrecall
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
HOST_TRIPLE := $(shell rustc -vV 2>/dev/null | sed -n 's/host: //p')

build:
	go build -tags "fts5 GO" -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/devrecall

run: build
	./bin/$(BINARY)

test:
	go test -tags "fts5 GO" ./... -race -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

desktop: build
	cd desktop && pnpm tauri dev

# Stage the CLI as a Tauri sidecar for the host target. Required before
# `pnpm tauri build` since tauri.conf.json declares externalBin and
# bundling fails if the matching `binaries/devrecall-<host-triple>` is
# missing.
desktop-cli: build
	@test -n "$(HOST_TRIPLE)" || (echo "rustc not found; install Rust toolchain" && exit 1)
	mkdir -p desktop/src-tauri/binaries
	cp bin/$(BINARY) desktop/src-tauri/binaries/devrecall-$(HOST_TRIPLE)
	chmod +x desktop/src-tauri/binaries/devrecall-$(HOST_TRIPLE)

desktop-bundle: desktop-cli
	cd desktop && pnpm tauri build

desktop-test:
	cd desktop && pnpm test

relay-deploy:
	cd relay && npx wrangler deploy

relay-test:
	cd relay && npx vitest run
