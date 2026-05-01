# Multi-stage build for security
FROM golang:1.26-alpine AS builder

# Install security tools
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./
RUN go mod download
RUN go mod verify

# Copy source code
COPY . .

# Build with security flags
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-s -w -extldflags "-static"' \
    -installsuffix cgo \
    -o relay-gateway ./cmd/relay-gateway

# Final minimal image
FROM scratch

# Copy CA certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binary
COPY --from=builder /app/relay-gateway /relay-gateway

# Use non-root user
USER 65534:65534

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/relay-gateway", "--health-check"]

# Run binary
ENTRYPOINT ["/relay-gateway"]
