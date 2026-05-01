GOHOSTOS := $(shell go env GOHOSTOS)
GOPATH := $(shell go env GOPATH)
VERSION := $(shell git describe --tags --always 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo dev)

ifeq ($(GOHOSTOS), windows)
Git_Bash := $(subst \,/,$(subst cmd\,bin\bash.exe,$(dir $(shell where git))))
INTERNAL_PROTO_FILES := $(shell $(Git_Bash) -c "find internal -name '*.proto'")
API_PROTO_FILES := $(shell $(Git_Bash) -c "find api -name '*.proto'")
else
INTERNAL_PROTO_FILES := $(shell find internal -name '*.proto')
API_PROTO_FILES := $(shell find api -name '*.proto')
endif

.PHONY: init
# init env
init:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/go-kratos/kratos/cmd/kratos/v2@latest
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@latest
	go install github.com/google/gnostic/cmd/protoc-gen-openapi@latest
	go install github.com/google/wire/cmd/wire@latest

.PHONY: config
# generate internal proto
config:
ifneq ($(strip $(INTERNAL_PROTO_FILES)),)
	protoc --proto_path=./internal \
		--proto_path=./third_party \
		--go_out=paths=source_relative:./internal \
		$(INTERNAL_PROTO_FILES)
else
	@echo "no internal proto files"
endif

.PHONY: api
# generate api proto
api:
ifneq ($(strip $(API_PROTO_FILES)),)
	protoc --proto_path=./api \
		--proto_path=./third_party \
		--go_out=paths=source_relative:./api \
		--go-http_out=paths=source_relative:./api \
		--go-grpc_out=paths=source_relative,require_unimplemented_servers=false:./api \
		$(API_PROTO_FILES)
else
	@echo "no api proto files"
endif

.PHONY: proto
# generate all proto
proto: api config

.PHONY: build
# build
build:
	go build ./...

.PHONY: generate
# generate
generate:
	go generate ./...
	go mod tidy

.PHONY: tidy
# tidy
tidy:
	go mod tidy

.PHONY: test
# test
test:
	go test ./...

.PHONY: run-identity
# run identity-service
run-identity:
	go run ./cmd/identity-service

.PHONY: run-channel
# run channel-service
run-channel:
	go run ./cmd/channel-service

.PHONY: run-relay
# run relay-gateway
run-relay:
	go run ./cmd/relay-gateway

.PHONY: all
# generate all
all: api config generate

.PHONY: help
# show help
help:
	@echo ''
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Targets:'
	@awk '/^[a-zA-Z\-\_0-9]+:/ { \
	helpMessage = match(lastLine, /^# (.*)/); \
	if (helpMessage) { \
	helpCommand = substr($$1, 0, index($$1, ":")); \
	helpMessage = substr(lastLine, RSTART + 2, RLENGTH); \
	printf "\033[36m%-22s\033[0m %s\n", helpCommand,helpMessage; \
	} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
