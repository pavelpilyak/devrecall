# DevRecall MCP server — container image used by glama.ai's automated
# health check. The entrypoint speaks MCP over stdio; the image is not
# intended for end-user deployment (DevRecall is local-first; the desktop
# install via `brew install --cask pavelpilyak/devrecall/devrecall` is the
# supported path).
#
# Sanity-check the build locally:
#   docker build -t devrecall-mcp .
#   docker run --rm -i devrecall-mcp <<< '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}'

FROM golang:1.26-alpine AS builder

# CGO is required for SQLite + FTS5 + sqlite-vec.
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /src

# Cache deps before copying the rest of the tree.
COPY go.mod go.sum ./
RUN go mod download

# Only the Go source paths the CLI compiles from. Desktop / docs / relay
# stay out of the build context.
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

FROM alpine:3.20

# ca-certificates so the embedder can lazily fetch its ONNX model from
# Hugging Face if `semantic_search_activities` is ever called. The MCP
# server starts fine without the model — only that one tool errors.
RUN apk add --no-cache ca-certificates \
 && addgroup -S devrecall \
 && adduser  -S devrecall -G devrecall

USER devrecall
WORKDIR /home/devrecall

# DevRecall opens ~/.devrecall/devrecall.db on startup and creates the
# file if absent; pre-create the directory so the FS perms are right.
RUN mkdir -p .devrecall

COPY --from=builder /out/devrecall /usr/local/bin/devrecall

# Stdio MCP server — no listening port, no exposed surface.
ENTRYPOINT ["devrecall", "mcp"]
