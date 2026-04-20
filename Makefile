.PHONY: build run test lint clean relay-deploy relay-test desktop desktop-test

BINARY := devrecall
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LICENSE_PUB_KEY := $(shell cat keys/prod-public.pem 2>/dev/null)

build:
	go build -tags "fts5 GO" -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/devrecall

build-prod:
	@test -f keys/prod-public.pem || (echo "Error: keys/prod-public.pem not found. Run: make keygen" && exit 1)
	go build -tags "fts5 GO" -ldflags "-X main.version=$(VERSION) -X 'github.com/pavelpilyak/devrecall/internal/license.publicKeyPEM=$(LICENSE_PUB_KEY)'" -o bin/$(BINARY) ./cmd/devrecall

keygen:
	@mkdir -p keys
	openssl genpkey -algorithm RSA -out keys/prod-private.pem -pkeyopt rsa_keygen_bits:2048
	openssl rsa -pubout -in keys/prod-private.pem -out keys/prod-public.pem
	@echo "Keypair generated in keys/. NEVER commit prod-private.pem."

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

desktop-test:
	cd desktop && pnpm test

relay-deploy:
	cd relay && npx wrangler deploy

relay-test:
	cd relay && npx vitest run
