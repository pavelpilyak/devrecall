# DevRecall MCP server — container image used by glama.ai's automated
# health check. The entrypoint speaks MCP over stdio; the image is not
# intended for end-user deployment (DevRecall is local-first; the desktop
# install via `brew install --cask pavelpilyak/devrecall/devrecall` is the
# supported path).
#
# Sanity-check the build locally:
#   docker build -t devrecall-mcp .
#   docker run --rm -i devrecall-mcp <<< '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}'
#
# Why debian-bookworm and not alpine: alpine's GCC 13 turns several
# pointer-cast warnings in sqlite-vec.c into errors. Bookworm ships GCC 12
# which is what the upstream CGO bindings test against.

FROM golang:1.26-bookworm AS builder

# CGO needs a C toolchain + SQLite headers (for FTS5).
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc libc6-dev libsqlite3-dev \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Cache deps before copying the rest of the tree.
COPY go.mod go.sum ./
RUN go mod download

# Only the Go source paths the CLI compiles from. Desktop / docs / relay
# stay out of the build context via .dockerignore.
COPY cmd/    ./cmd/
COPY internal/ ./internal/
COPY pkg/    ./pkg/

ENV CGO_ENABLED=1
RUN go build \
    -tags 'fts5 GO' \
    -ldflags '-s -w' \
    -o /out/devrecall \
    ./cmd/devrecall

# ---- runtime ----

FROM debian:bookworm-slim

# ca-certificates so the embedder can lazily fetch its ONNX model from
# Hugging Face if `semantic_search_activities` is ever called. The MCP
# server starts fine without the model — only that one tool errors.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -r devrecall \
 && useradd  -r -g devrecall -m -d /home/devrecall devrecall

USER devrecall
WORKDIR /home/devrecall

COPY --from=builder /out/devrecall /usr/local/bin/devrecall

# Bootstrap a default config + empty DB so `devrecall mcp` starts cleanly
# in glama.ai's sandbox (where no user has run `config init` yet). The
# server still works on real installs — it opens the user's existing
# ~/.devrecall/devrecall.db on startup.
RUN devrecall config init

# Stdio MCP server — no listening port, no exposed surface.
ENTRYPOINT ["devrecall", "mcp"]
