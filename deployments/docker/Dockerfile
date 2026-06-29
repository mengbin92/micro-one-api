# Multi-stage build for security
# Usage: docker build --build-arg SERVICE_NAME=relay-gateway -f Dockerfile ../..
ARG SERVICE_NAME=relay-gateway

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

# build-base pulls gcc + musl-dev, required for CGO (mattn/go-sqlite3).
# sqlite-libs is the runtime dep for the SQLite3 driver, shipped in the
# alpine final image.
RUN apk add --no-cache git ca-certificates nodejs npm make protobuf protobuf-dev build-base sqlite-libs

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG SERVICE_NAME
ARG TARGETOS
ARG TARGETARCH
RUN make proto-tools && make proto

RUN if [ "$SERVICE_NAME" = "admin-api" ]; then \
      cd web && npm ci && npm run build && \
      rm -rf /app/internal/admin/server/static/web && \
      mkdir -p /app/internal/admin/server/static/web && \
      cp -r /app/web/dist/* /app/internal/admin/server/static/web/; \
    fi

# CGO is required by github.com/mattn/go-sqlite3. Build with CGO_ENABLED=1
# so the lite deployment (DATABASE_DRIVER=sqlite3) works.
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags='-s -w -extldflags "-static"' \
    -o /app/bin/service ./cmd/${SERVICE_NAME}

# Final image: alpine instead of scratch because CGO-linked binaries
# depend on the musl libc and libsqlite3. The image is still small
# (alpine:3.20 + sqlite-libs) and runs as the unprivileged 65534 user.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/bin/service /service
COPY --from=builder /app/configs /configs

USER 65534:65534

EXPOSE 8080

ENTRYPOINT ["/service"]
