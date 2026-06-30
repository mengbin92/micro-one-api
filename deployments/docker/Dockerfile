# Multi-stage build for security
# Usage: docker build --build-arg SERVICE_NAME=relay-gateway -f Dockerfile ../..
ARG SERVICE_NAME=relay-gateway

# ---------------------------------------------------------------------------
# Stage 1: web-builder
# Builds the admin frontend (vite 8 requires node >= 20.19, alpine 3.20 only
# ships node 20.14). Using a dedicated node:24 stage keeps the Go build
# stage small and avoids relying on the OS-shipped node.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web-builder

WORKDIR /web

# Copy lockfile + manifests first so dependency install caches independently
# of the source tree. This step fails fast if package-lock.json is out of
# sync with package.json (which would otherwise fail much later in
# `npm run build` and waste CI time).
COPY web/package.json web/package-lock.json web/.npmrc ./

# `npm ci` requires a clean install. We don't have node_modules in the
# build context (.dockerignore excludes it), so this is always a fresh
# install. --no-audit --no-fund keep logs quiet and fast.
RUN npm ci --no-audit --no-fund

COPY web/ ./

RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: go-builder
# Builds the Go service binary. This stage runs on the TARGET platform
# (natively, or emulated via QEMU on an amd64 GitHub runner), NOT on the
# build platform. That way the CGO toolchain (mattn/go-sqlite3) always
# matches the target arch and no fragile cross-sysroot is needed.
# docker/setup-qemu-action (configured in the CI workflow) supplies the
# emulation, so linux/arm64 is handled transparently.
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

# build-base pulls gcc + musl-dev for CGO (mattn/go-sqlite3); sqlite-dev
# provides the headers/libs libsqlite3 links against, and sqlite-libs is
# the runtime dep shipped in the final alpine image.
RUN apk add --no-cache \
      git ca-certificates make protobuf protobuf-dev \
      build-base sqlite-libs sqlite-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG SERVICE_NAME

RUN make proto-tools && make proto

# For admin-api, the web dist was built in the dedicated web-builder stage
# (alpine's node 20.14 is too old for vite 8). Bring it in here so the
# //go:embed all:static/web directive picks it up at build time. The
# intermediate /tmp/web-dist path lets us guard the COPY with a shell
# conditional on SERVICE_NAME.
COPY --from=web-builder /web/dist /tmp/web-dist
RUN if [ "$SERVICE_NAME" = "admin-api" ]; then \
      rm -rf /app/internal/admin/server/static/web && \
      mkdir -p /app/internal/admin/server/static/web && \
      cp -a /tmp/web-dist/. /app/internal/admin/server/static/web/; \
    fi

# CGO is required by github.com/mattn/go-sqlite3 so the lite deployment
# (DATABASE_DRIVER=sqlite3) works. Because this stage runs on the target
# platform, Go's host detection yields the correct GOOS/GOARCH and the
# platform-native gcc is used — no manual CC/CXX selection is needed.
RUN CGO_ENABLED=1 go build \
      -ldflags='-s -w -extldflags "-static"' \
      -o /app/bin/service ./cmd/${SERVICE_NAME}

# ---------------------------------------------------------------------------
# Stage 3: runtime
# Minimal alpine image with sqlite-libs (CGO-linked binaries need musl
# libc + libsqlite3 at runtime). Runs as the unprivileged 65534 user.
# ---------------------------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/bin/service /service
COPY --from=builder /app/configs /configs

USER 65534:65534

EXPOSE 8080

ENTRYPOINT ["/service"]
