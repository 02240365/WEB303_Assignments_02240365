# =============================================================================
# Route Service — multi-stage Dockerfile
# Build context is the PROJECT ROOT (see docker-compose.yml) because the service
# uses the shared go.mod, generated protobuf code (gen/) and helpers (pkg/).
# =============================================================================

# ---------- Stage 1: build ----------------------------------------------------
# Full Go toolchain (~250 MB) — used only to compile, then thrown away.
FROM golang:1.23-alpine AS builder
WORKDIR /src

# Download dependencies first so Docker can cache this layer.
COPY go.mod go.sum ./
RUN go mod download

# Copy only the code this service needs.
COPY gen/ gen/
COPY pkg/ pkg/
COPY route-service/ route-service/

# Static binary (no C libraries needed), debug info stripped to save space.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/route-service ./route-service

# ---------- Stage 2: run ------------------------------------------------------
# Tiny runtime image (~15 MB) that contains just the compiled binary.
FROM alpine:3.20
RUN adduser -D -H appuser
USER appuser
COPY --from=builder /out/route-service /app/route-service

# Documentation only — the real port comes from ROUTE_GRPC_PORT.
EXPOSE 50051
ENTRYPOINT ["/app/route-service"]
