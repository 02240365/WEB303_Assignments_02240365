# =============================================================================
# API Gateway — multi-stage Dockerfile
# Build context is the PROJECT ROOT (see docker-compose.yml).
# =============================================================================

# ---------- Stage 1: build ----------------------------------------------------
FROM golang:1.23-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY gen/ gen/
COPY pkg/ pkg/
COPY gateway/ gateway/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gateway ./gateway

# ---------- Stage 2: run ------------------------------------------------------
FROM alpine:3.20
RUN adduser -D -H appuser
USER appuser
COPY --from=builder /out/gateway /app/gateway

EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
