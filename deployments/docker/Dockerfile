# Multi-stage build for security
# Usage: docker build --build-arg SERVICE_NAME=relay-gateway -f Dockerfile ../..
ARG SERVICE_NAME=relay-gateway

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG SERVICE_NAME
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-s -w -extldflags "-static"' \
    -installsuffix cgo \
    -o /app/bin/service ./cmd/${SERVICE_NAME}

# Final minimal image
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/bin/service /service
COPY --from=builder /app/configs /configs

USER 65534:65534

EXPOSE 8080

ENTRYPOINT ["/service"]
