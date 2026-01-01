# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=1 go build -o weirdstats ./cmd/weirdstats

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /app/weirdstats .

# Create data directory for SQLite database
RUN mkdir -p /data

ENV DATABASE_PATH=/data/weirdstats.db
ENV SERVER_ADDR=:8080

EXPOSE 8080

CMD ["./weirdstats"]
