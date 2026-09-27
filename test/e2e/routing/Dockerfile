# Isolated acceptance image: real service entrypoints share one build/cache.
# The acceptance test binary is compiled from the working tree in this image,
# so --skip-build must NOT be used after editing anything under test/.
FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
RUN apk add --no-cache git ca-certificates make build-base
ENV GOPROXY=https://proxy.golang.org|https://goproxy.cn|direct
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY Makefile ./
COPY scripts/tool-versions.env scripts/tool-versions.env
RUN make init
COPY . .
RUN make proto
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=1 go build -p 2 -o /out/ \
    ./cmd/relay-gateway ./cmd/migrate ./app/admin/cmd/admin ./app/identity/cmd/identity \
    ./app/channel/cmd/channel ./app/billing/cmd/billing ./app/config/cmd/config \
    ./app/log/cmd/log ./app/monitor/cmd/monitor ./app/notify/cmd/notify \
    ./app/channel/cmd/group-audit ./app/channel/cmd/group-backfill \
    ./app/identity/cmd/routing-backfill ./scripts/routing-preflight ./test/e2e/routingmock
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=1 go test -c -p 2 -o /out/routing-e2e ./test/e2e/routing

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates sqlite-libs
COPY --from=build /out /out
COPY --from=build /app/configs /app/configs
COPY --from=build /app/app /app/app
COPY --from=build /app/migrations /app/migrations
USER 65534:65534
