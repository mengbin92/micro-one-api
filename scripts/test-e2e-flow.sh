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
E2E_DIR="$PROJECT_ROOT/test/e2e"
ENV_FILE="$COMPOSE_DIR/.env"

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

cleanup() {
    log "Tearing down..."
    cd "$COMPOSE_DIR"
    docker-compose -f "$COMPOSE_FILE" -f "$COMPOSE_TEST" down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

load_env_var() {
    local key="$1"
    local value
    value=$(grep -E "^${key}=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true)
    if [ -n "$value" ] && [ -z "${!key:-}" ]; then
        export "$key=$value"
    fi
}

if [ -f "$ENV_FILE" ]; then
    load_env_var "MYSQL_ROOT_PASSWORD"
    load_env_var "ADMIN_TOKEN"
fi

# ── Step 0: Start environment ──

cleanup

log "Starting docker-compose with test override..."
cd "$COMPOSE_DIR"
docker-compose -f "$COMPOSE_FILE" -f "$COMPOSE_TEST" up -d --build 2>&1 | tail -5

log "Waiting for services to be ready..."

# Wait for infrastructure
wait_healthy() {
    local container="$1"
    local max="${2:-30}"
    for i in $(seq 1 "$max"); do
        local status
        status=$(docker inspect --format='{{.State.Health.Status}}' "$container" 2>/dev/null || echo "missing")
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
        state=$(docker inspect --format='{{.State.Status}}' "$container" 2>/dev/null || echo "missing")
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
docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -e "
INSERT INTO channels (
    type, \`key\`, status, name, base_url, models, \`group\`, priority, config,
    weight, created_time, test_time, response_time, balance, balance_updated_time,
    used_quota, model_mapping, system_prompt
)
SELECT
    1, 'sk-mock-key', 1, 'e2e-mock-openai', 'http://mock-upstream:9999',
    'gpt-3.5-turbo,gpt-4', 'default', 1, '{}',
    1, UNIX_TIMESTAMP(), 0, 0, 0, 0,
    0, '', NULL
WHERE NOT EXISTS (
    SELECT 1 FROM channels WHERE name = 'e2e-mock-openai'
);

UPDATE channels
SET
    type = 1,
    \`key\` = 'sk-mock-key',
    status = 1,
    base_url = 'http://mock-upstream:9999',
    models = 'gpt-3.5-turbo,gpt-4',
    \`group\` = 'default',
    priority = 1,
    weight = 1
WHERE name = 'e2e-mock-openai';

SET @channel_id := (SELECT id FROM channels WHERE name = 'e2e-mock-openai' LIMIT 1);

DELETE FROM abilities WHERE channel_id = @channel_id;
INSERT INTO abilities (\`group\`, model, channel_id, enabled, priority)
VALUES
    ('default', 'gpt-3.5-turbo', @channel_id, 1, 1),
    ('default', 'gpt-4', @channel_id, 1, 1);
" 2>/dev/null

# Verify test channel
channel_status=$(docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -N -e \
    "SELECT status FROM channels WHERE name='e2e-mock-openai' LIMIT 1;" 2>/dev/null)
channel_url=$(docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -N -e \
    "SELECT base_url FROM channels WHERE name='e2e-mock-openai' LIMIT 1;" 2>/dev/null)
ability_count=$(docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -N -e \
    "SELECT COUNT(*) FROM abilities a JOIN channels c ON c.id=a.channel_id WHERE c.name='e2e-mock-openai' AND a.enabled=1;" 2>/dev/null)
if [ "$channel_status" = "1" ] && [ "$channel_url" = "http://mock-upstream:9999" ] && [ "$ability_count" -ge 2 ]; then
    log "Test channel ready: $channel_url"
else
    fail "Test channel setup failed: status='$channel_status', url='$channel_url', abilities='$ability_count'"
    exit 1
fi

# ── Step 2: Run tests ──

if [ "$RUN_SUITE" = true ]; then
    # Run Go test suite
    log "Running Go test suite..."
    cd "$PROJECT_ROOT"
    env_args=(
        "ADMIN_TOKEN=${ADMIN_TOKEN:-test-admin-token-for-dev}"
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
    go build -o "$E2E_DIR/e2e-test" "$E2E_DIR/main.go"

    # Run register+login only first to get user_id and token
    log "Registering test user..."
    REGISTER_OUTPUT=$("$E2E_DIR/e2e-test" --step register 2>&1) || true
    echo "$REGISTER_OUTPUT"

    # Extract user_id from register output
    E2E_USER_ID=$(echo "$REGISTER_OUTPUT" | sed -n 's/.*user_id=\([0-9]*\).*/\1/p' | head -1)
    if [ -z "$E2E_USER_ID" ]; then
        fail "Could not extract user_id from registration"
        exit 1
    fi
    log "Registered user_id=$E2E_USER_ID"

    # Set quota for the test user (registration doesn't set default quota)
    log "Setting quota for test user..."
    docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}" oneapi -e \
        "UPDATE users SET quota=1000000 WHERE id=$E2E_USER_ID;" 2>/dev/null
    log "Quota set to 1000000"

    # Run full E2E test
    log "Running full E2E test..."
    "$E2E_DIR/e2e-test" --step all
    E2E_EXIT=$?

    # Cleanup binary
    rm -f "$E2E_DIR/e2e-test"

    exit $E2E_EXIT
fi
