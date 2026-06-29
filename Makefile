GOHOSTOS := $(shell go env GOHOSTOS)
GOPATH := $(shell go env GOPATH)
VERSION := $(shell git describe --tags --always 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo dev)

ifeq ($(GOHOSTOS), windows)
Git_Bash := $(subst \,/,$(subst cmd\,bin\bash.exe,$(dir $(shell where git))))
INTERNAL_PROTO_FILES := $(shell $(Git_Bash) -c "find internal -name '*.proto'")
API_PROTO_FILES := $(shell $(Git_Bash) -c "find api -name '*.proto' ! -path 'api/openapi.yaml'")
else
INTERNAL_PROTO_FILES := $(shell find internal -name '*.proto')
API_PROTO_FILES := $(shell find api -name '*.proto')
endif
PROTO_SYSTEM_INCLUDE_DIRS := $(strip $(foreach dir,/usr/include /usr/local/include /opt/homebrew/include,$(if $(wildcard $(dir)/google/protobuf/descriptor.proto),--proto_path=$(dir))))

.PHONY: init
# init env
init:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	go install github.com/go-kratos/kratos/cmd/kratos/v2@latest
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@latest
	go install github.com/google/gnostic/cmd/protoc-gen-openapi@latest
	go install github.com/google/wire/cmd/wire@latest

.PHONY: proto-tools
# install protobuf generators needed by make proto
proto-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@latest
	go install github.com/google/gnostic/cmd/protoc-gen-openapi@latest

.PHONY: config
# generate internal proto
config:
ifneq ($(strip $(INTERNAL_PROTO_FILES)),)
	protoc --proto_path=./internal \
		--proto_path=./third_party \
		$(PROTO_SYSTEM_INCLUDE_DIRS) \
		--go_out=paths=source_relative:./internal \
		$(INTERNAL_PROTO_FILES)
else
	@echo "no internal proto files"
endif

.PHONY: api
# generate api proto
api:
ifneq ($(strip $(API_PROTO_FILES)),)
	protoc \
		--proto_path=. \
		--proto_path=./third_party \
		$(PROTO_SYSTEM_INCLUDE_DIRS) \
		--go_out=paths=source_relative:. \
		--go-http_out=paths=source_relative:. \
		--go-grpc_out=paths=source_relative,require_unimplemented_servers=false:. \
		--grpc-gateway_out=paths=source_relative:. \
		--openapi_out=fq_schema_naming=true,default_response=false,naming=json:. \
		$(API_PROTO_FILES)
else
	@echo "no api proto files"
endif

.PHONY: api-check
# verify generated OpenAPI output
api-check: api
	@test -s openapi.yaml
	@grep -q "/v1/chat/completions" openapi.yaml
	@test ! -e api/openapi.yaml

.PHONY: proto
# generate all proto
proto: api config

.PHONY: build
# build
build: proto web-build
	go build ./...

.PHONY: web-dist
# build web frontend into web/dist for external ADMIN_WEB_ROOT deployments
web-dist:
	cd web && npm ci && npm run build

.PHONY: web-build
# build web frontend and copy it into the embedded admin-api asset directory
web-build: web-dist
	rm -rf internal/admin/server/static/web
	mkdir -p internal/admin/server/static/web
	cp -r web/dist/* internal/admin/server/static/web/

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
# run tests that do not require externally started services
test: test-unit

.PHONY: test-unit
# run unit and integration tests, excluding local-service e2e suite
test-unit: proto
	go test $$(go list ./... | grep -v '/test/e2e/suite$$' | grep -v '/web/node_modules/')

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

.PHONY: test-e2e
# run e2e tests (docker-compose environment)
test-e2e:
	./scripts/test-e2e-flow.sh

.PHONY: test-e2e-suite
# run e2e Go test suite (docker-compose environment)
test-e2e-suite:
	./scripts/test-e2e-flow.sh --suite

.PHONY: test-e2e-local
# run e2e Go test suite against local services (no docker)
test-e2e-local:
	go test -v -count=1 -timeout 120s ./test/e2e/suite/

.PHONY: clean
# clean build artifacts and logs
clean:
	rm -rf bin/
	rm -rf logs/

.PHONY: migrate
# apply pending DB migrations; reads MIGRATIONS_DSN or SQL_DSN
migrate:
	go run ./cmd/migrate -dir ./migrations

.PHONY: migrate-status
# print migration status without applying anything
migrate-status:
	go run ./cmd/migrate -dir ./migrations -status

.PHONY: security-scan
# run security scanning
security-scan:
	@echo "Running security scans..."
	@echo "1. Running gosec (SAST)..."
	@if ! command -v gosec &> /dev/null; then \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	fi
	@gosec -exclude-generated -exclude=G104 -exclude-dir=web/node_modules ./... || echo "gosec found issues"
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
	@gosec -fmt json -out gosec-report.json -exclude-generated -exclude=G104 -exclude-dir=web/node_modules ./...

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
