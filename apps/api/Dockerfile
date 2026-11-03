# syntax=docker/dockerfile:1
# Multi-stage build for the Go API. Final image is distroless and non-root.
# Build context = apps/api (see compose.yaml).

# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads separately from the source. The cache mount keeps
# downloaded modules across builds; GOPROXY fallback direct handles a flaky
# proxy.golang.org (TLS handshake timeouts observed 2026-10-27).
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    GOPROXY="https://proxy.golang.org,direct" \
    go mod download

COPY . .
# Static, stripped binary. arm64 is native on Apple Silicon.
RUN --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/server ./cmd/server

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/server /app/server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
