# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source code
COPY . .

# Build the binary
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o weirdstats ./cmd/weirdstats

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /app/weirdstats .

# Create data directory for SQLite database
RUN mkdir -p /data \
    && adduser -D -H -u 10001 app \
    && chown -R app:app /data /app

USER app

ENV DATABASE_PATH=/data/weirdstats.db
ENV SERVER_ADDR=:8080

VOLUME ["/data"]

EXPOSE 8080

CMD ["./weirdstats"]
