# =============================================================================
# Permit Service — multi-stage Dockerfile
# Build context is the PROJECT ROOT (see docker-compose.yml).
# =============================================================================

# ---------- Stage 1: build ----------------------------------------------------
FROM golang:1.23-alpine AS builder
WORKDIR /src

# Cache dependency downloads in their own layer.
COPY go.mod go.sum ./
RUN go mod download

COPY gen/ gen/
COPY pkg/ pkg/
COPY permit-service/ permit-service/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/permit-service ./permit-service

# ---------- Stage 2: run ------------------------------------------------------
FROM alpine:3.20
RUN adduser -D -H appuser
USER appuser
COPY --from=builder /out/permit-service /app/permit-service

EXPOSE 50052
ENTRYPOINT ["/app/permit-service"]
