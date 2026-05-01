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
	IDENTITY_GRPC_ADDR=127.0.0.1:9001 \
	IDENTITY_SQL_DSN="" \
	go run ./cmd/identity-service

.PHONY: run-channel
# run channel-service
run-channel:
	CHANNEL_GRPC_ADDR=127.0.0.1:9002 \
	CHANNEL_SQL_DSN="" \
	go run ./cmd/channel-service

.PHONY: run-relay
# run relay-gateway
run-relay:
	IDENTITY_GRPC_ENDPOINT=127.0.0.1:9001 \
	CHANNEL_GRPC_ENDPOINT=127.0.0.1:9002 \
	RELAY_HTTP_ADDR=:8080 \
	RELAY_PROVIDER_TIMEOUT=30s \
	go run ./cmd/relay-gateway

.PHONY: run-all
# run all services
run-all:
	@echo "Starting all services in background..."
	@mkdir -p logs
	@make run-identity > logs/identity.log 2>&1 &
	@echo "Identity service started (PID: $$!)"
	@sleep 2
	@make run-channel > logs/channel.log 2>&1 &
	@echo "Channel service started (PID: $$!)"
	@sleep 2
	@make run-relay > logs/relay.log 2>&1 &
	@echo "Relay gateway started (PID: $$!)"
	@echo "All services started. Check logs in ./logs/ directory."
	@echo "To stop all services, run: make stop-all"

.PHONY: stop-all
# stop all services
stop-all:
	@echo "Stopping all services..."
	@pkill -f "identity-service" || true
	@pkill -f "channel-service" || true
	@pkill -f "relay-gateway" || true
	@echo "All services stopped."

.PHONY: dev-test-identity
# test identity-service
dev-test-identity:
	go test -v ./internal/identity/biz/

.PHONY: dev-test-channel
# test channel-service
dev-test-channel:
	go test -v ./internal/channel/biz/

.PHONY: dev-test-provider
# test relay provider
dev-test-provider:
	go test -v ./internal/relay/provider/

.PHONY: dev-test-integration
# run integration tests
dev-test-integration:
	go test -v ./test/integration/

.PHONY: dev-test-all
# run all development tests
dev-test-all:
	@echo "Running all development tests..."
	@make dev-test-identity
	@make dev-test-channel
	@make dev-test-provider
	@make dev-test-integration
	@echo "All tests completed!"

.PHONY: clean
# clean build artifacts and logs
clean:
	rm -rf bin/
	rm -rf logs/

.PHONY: security-scan
# run security scanning
security-scan:
	@echo "Running security scans..."
	@echo "1. Running gosec (SAST)..."
	@if ! command -v gosec &> /dev/null; then \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	fi
	@gosec ./... || echo "gosec found issues"
	@echo "2. Running govulncheck (SCA)..."
	@if ! command -v govulncheck &> /dev/null; then \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	@govulncheck ./... || echo "govulncheck found vulnerabilities"
	@echo "3. Running gitleaks (secret scanning)..."
	@if ! command -v gitleaks &> /dev/null; then \
		go install github.com/zricethezav/gitleaks/v8/cmd/gitleaks@latest; \
	fi
	@gitleaks detect --source . || echo "gitleaks found secrets"
	@echo "Security scans completed!"

.PHONY: security-sast
# run static application security testing
security-sast:
	@if ! command -v gosec &> /dev/null; then \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	fi
	@gosec -fmt json -out gosec-report.json ./...

.PHONY: security-sca
# run software composition analysis
security-sca:
	@if ! command -v govulncheck &> /dev/null; then \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	@govulncheck -json ./... > vulncheck-report.json

.PHONY: security-secrets
# scan for secrets in code
security-secrets:
	@if ! command -v gitleaks &> /dev/null; then \
		go install github.com/zricethezav/gitleaks/v8/cmd/gitleaks@latest; \
	fi
	@gitleaks detect --source . --verbose --report-path gitleaks-report.json

.PHONY: security-sbom
# generate software bill of materials
security-sbom:
	@if ! command -v syft &> /dev/null; then \
		go install github.com/anchore/syft/cmd/syft@latest; \
	fi
	@syft . -o spdx-json > sbom.json
	@echo "SBOM generated: sbom.json"

.PHONY: security-check
# comprehensive security check
security-check: security-sast security-sca security-secrets security-sbom
	@echo "Comprehensive security check completed!"
	@echo "Reports generated:"
	@echo "  - gosec-report.json"
	@echo "  - vulncheck-report.json"
	@echo "  - gitleaks-report.json"
	@echo "  - sbom.json"

.PHONY: security-fix
# attempt to fix common security issues
security-fix:
	@echo "Running security fixes..."
	@echo "1. Checking for hardcoded credentials..."
	@! grep -r "password\|secret\|token\|api[_-]?key" --include="*.go" --include="*.yaml" --include="*.yml" . | grep -v ".git/" | grep -v "vendor/" | grep -v "test/" || echo "Potential hardcoded credentials found - please review manually"
	@echo "2. Checking for insecure HTTP..."
	@! grep -r "http://" --include="*.go" --include="*.yaml" . | grep -v ".git/" | grep -v "vendor/" || echo "Insecure HTTP usage found - please review manually"
	@echo "3. Checking for fmt.Printf in production code..."
	@! grep -r "fmt.Printf" --include="*.go" . | grep -v ".git/" | grep -v "vendor/" | grep -v "test/" || echo "fmt.Printf found in production code - please use structured logging"
	@echo "Security fix check completed!"

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
