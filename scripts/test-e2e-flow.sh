#!/usr/bin/env bash
# End-to-end flow test: Register -> Login -> Models -> Chat Completion -> Billing
#
# Usage:
#   ./scripts/test-e2e-flow.sh              # Run standalone binary flow
#   ./scripts/test-e2e-flow.sh --suite      # Run Go test suite (go test)
#
# Prerequisites:
#   - Docker and docker-compose installed
#   - Go toolchain installed (to build test binary)
#   - curl and Python 3 installed (to prepare the mock channel through HTTP)
#   - Ports 3000, 8080, 8001, 9001, 9002, 9004 available
#
# This script:
#   1. Starts services with test override (exposes gRPC ports, adds mock upstream)
#   2. Updates test channel to point to mock upstream
#   3. Builds and runs E2E tests (register, login, models, chat, billing, admin)
#   4. Tears down the environment

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_DIR="$PROJECT_ROOT/deployments/docker-compose"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
COMPOSE_TEST="$COMPOSE_DIR/docker-compose.test.yml"
ENV_FILE="$COMPOSE_DIR/.env.example"
project="micro-one-api-e2e-${GITHUB_RUN_ID:-$$}-${GITHUB_RUN_ATTEMPT:-1}"
identity_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-identities.XXXXXX")"
run_env="$identity_dir/compose.env"

# Parse flags
RUN_SUITE=false
for arg in "$@"; do
  case "$arg" in
    --suite) RUN_SUITE=true ;;
  esac
done

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[E2E]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }

# GitHub Actions runners ship `docker compose` (v2 plugin) but not the legacy
# `docker-compose` standalone binary; local dev machines often have only the
# legacy one. Prefer the legacy binary when present, otherwise fall back to
# the v2 plugin so the same script works in both environments.
if command -v docker-compose >/dev/null 2>&1; then
    compose=(docker-compose)
else
    compose=(docker compose)
fi
# Pass Docker client settings only. Deployment variables inherited from the shell
# would take precedence over the isolated test env file during interpolation.
docker_env=(env -i "HOME=$HOME" "PATH=$PATH" "COMPOSE_PARALLEL_LIMIT=${COMPOSE_PARALLEL_LIMIT:-2}")
for key in ${!DOCKER_@} ${!BUILDX_@} SSH_AUTH_SOCK XDG_RUNTIME_DIR TMPDIR; do
    if [[ -n "${!key:-}" ]]; then docker_env+=("$key=${!key}"); fi
done
compose=("${docker_env[@]}" "${compose[@]}" -p "$project" --env-file "$run_env" -f "$COMPOSE_FILE" -f "$COMPOSE_TEST")
log "Using test project: $project"

cleanup() {
    "${compose[@]}" down -v --remove-orphans
}

on_exit() {
    local code=$?
    if [ -n "${E2E_KEEP_ON_FAILURE:-}" ] && [ "$code" -ne 0 ]; then
        warn "Keeping $project for failure diagnostics"
    elif [ -f "$run_env" ]; then
        cleanup || code=1
    fi
    rm -rf "$identity_dir"
    exit "$code"
}
trap on_exit EXIT

# Match the test services to the checked-in fixture, even when a local
# deployment exports its own database URLs or credentials.
while IFS='=' read -r key value; do
    case "$key" in
        MYSQL_ROOT_PASSWORD|DATABASE_DSN|*_SQL_DSN|ADMIN_TOKEN|SERVICE_TOKEN|CHANNEL_ENCRYPTION_KEY)
            export "$key=$value" ;;
    esac
done < "$ENV_FILE"

# Execution claims require independent callers, including in legacy IAM mode.
mkdir "$identity_dir/certs"
python3 "$SCRIPT_DIR/prepare-rbac-service-identities.py" --output "$identity_dir/identities.env"
cp "$ENV_FILE" "$run_env"
printf '\nALIPAY_CERT_DIR="%s/certs"\nWECHAT_CERT_DIR="%s/certs"\n' "$identity_dir" "$identity_dir" >> "$run_env"
cat "$identity_dir/identities.env" >> "$run_env"

# ── Step 0: Start environment ──

log "Starting docker-compose with test override..."
cd "$COMPOSE_DIR"
compose_log="$identity_dir/compose.log"
if ! "${compose[@]}" up -d --build 2>&1 | tee "$compose_log"; then
	tail -20 "$compose_log"
	warn "docker-compose startup failed; retrying once after health checks settle..."
	sleep 5
	"${compose[@]}" up -d --build 2>&1 | tee "$compose_log"
fi
rm -f "$compose_log"

log "Waiting for services to be ready..."

# Wait for infrastructure
wait_healthy() {
    local container="$1"
    local max="${2:-30}"
    for i in $(seq 1 "$max"); do
        local status
        status=$(docker inspect --format='{{.State.Health.Status}}' "$("${compose[@]}" ps -q "$container")" 2>/dev/null || echo "missing")
        if [ "$status" = "healthy" ]; then
            return 0
        fi
        sleep 2
    done
    fail "$container not healthy after ${max} attempts"
    return 1
}

wait_running() {
    local container="$1"
    local max="${2:-30}"
    for i in $(seq 1 "$max"); do
        local state
        state=$(docker inspect --format='{{.State.Status}}' "$("${compose[@]}" ps -q "$container")" 2>/dev/null || echo "missing")
        if [ "$state" = "running" ]; then
            return 0
        fi
        sleep 2
    done
    fail "$container not running after ${max} attempts"
    return 1
}

wait_healthy "mysql" 30
wait_healthy "redis" 30
wait_healthy "mock-upstream" 15
wait_running "identity-service" 20
wait_running "channel-service" 20
wait_running "billing-service" 20
wait_running "relay-gateway" 20

log "All services ready."

# ── Step 1: Prepare test channel pointing to mock upstream ──

log "Preparing test channel for mock-upstream..."
# Create through the owner so credentials and model registry mappings use the
# same persistence rules as real channels.
curl --fail --silent --show-error --retry 10 --retry-all-errors --retry-delay 1 \
    --retry-max-time 30 --max-time 10 http://127.0.0.1:3000/healthz > /dev/null
channel_response=$(curl --fail --silent --show-error --max-time 30 \
    -H "Authorization: Bearer ${ADMIN_TOKEN:?ADMIN_TOKEN is required}" \
    -H 'Content-Type: application/json' \
    --data '{"type":1,"key":"sk-mock-key","name":"e2e-mock-openai","base_url":"http://mock-upstream:9999","models":"gpt-3.5-turbo,gpt-4","group":"default","priority":1,"weight":1,"config":{},"reason":"E2E mock channel fixture"}' \
    http://127.0.0.1:3000/api/channel)
printf '%s' "$channel_response" | python3 -c '
import json, sys
reply = json.load(sys.stdin)
if reply.get("success") is not True or int((reply.get("data") or {}).get("channel_id", 0)) <= 0:
    raise SystemExit("mock channel creation through admin API failed")
'

# Verify test channel
channel_status=$("${compose[@]}" exec -T mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -N -e \
    "SELECT status = 1 AND \`key\` <> 'sk-mock-key' FROM channels WHERE name='e2e-mock-openai' LIMIT 1;" 2>/dev/null)
channel_url=$("${compose[@]}" exec -T mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -N -e \
    "SELECT base_url FROM channels WHERE name='e2e-mock-openai' LIMIT 1;" 2>/dev/null)
mapping_count=$("${compose[@]}" exec -T mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -N -e \
    "SELECT COUNT(*) FROM model_channel_mapping mcm JOIN channels c ON c.id=mcm.channel_id WHERE c.name='e2e-mock-openai' AND mcm.enabled=1;" 2>/dev/null)
if [ "$channel_status" = "1" ] && [ "$channel_url" = "http://mock-upstream:9999" ] && [ "$mapping_count" -ge 2 ]; then
    log "Test channel ready: $channel_url"
else
    fail "Test channel setup failed: status='$channel_status', url='$channel_url', mappings='$mapping_count'"
    exit 1
fi

# ── Step 2: Run tests ──

if [ "$RUN_SUITE" = true ]; then
    # Run Go test suite
    log "Running Go test suite..."
    cd "$PROJECT_ROOT"
    env_args=(
        "IDENTITY_GRPC_ENDPOINT=127.0.0.1:9001" "BILLING_GRPC_ENDPOINT=127.0.0.1:9004"
        "RELAY_HTTP_BASE=http://127.0.0.1:8080" "ADMIN_HTTP_BASE=http://127.0.0.1:3000"
        "ADMIN_TOKEN=${ADMIN_TOKEN:-test-admin-token-for-dev}"
        "SERVICE_TOKEN=${SERVICE_TOKEN:-change-me-to-a-long-random-string}"
    )
    [ -n "${PROVIDER_API_KEY:-}" ] && env_args+=("PROVIDER_API_KEY=$PROVIDER_API_KEY")
    [ -n "${PROVIDER_BASE_URL:-}" ] && env_args+=("PROVIDER_BASE_URL=$PROVIDER_BASE_URL")
    [ -n "${PROVIDER_MODEL:-}" ] && env_args+=("PROVIDER_MODEL=$PROVIDER_MODEL")
    [ -n "${TEST_MODEL:-}" ] && env_args+=("TEST_MODEL=$TEST_MODEL")

    env "${env_args[@]}" \
        go test -v -count=1 -timeout 120s ./test/e2e/suite/ \
        || { fail "Go test suite failed"; exit 1; }
    log "Go test suite passed!"
else
    # Run standalone binary flow (original behavior)
    log "Building E2E test binary..."
    cd "$PROJECT_ROOT"
    go build -o "$identity_dir/e2e-test" "$PROJECT_ROOT/test/e2e/main.go"

    log "Registering test user..."
    "$identity_dir/e2e-test" --step register
    log "Running full E2E test..."
    "$identity_dir/e2e-test" --step all
fi
