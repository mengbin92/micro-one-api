#!/usr/bin/env bash
# End-to-end flow test: Register -> Login -> Models -> Chat Completion -> Billing
#
# Usage: ./scripts/test-e2e-flow.sh
#
# Prerequisites:
#   - Docker and docker-compose installed
#   - Go toolchain installed (to build test binary)
#   - Ports 3000, 8080, 8001, 9001, 9002, 9004 available
#
# This script:
#   1. Starts services with test override (exposes gRPC ports, adds mock upstream)
#   2. Updates test channel to point to mock upstream
#   3. Builds and runs Go E2E test (register, login, models, chat, billing)
#   4. Tears down the environment

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_DIR="$PROJECT_ROOT/deployments/docker-compose"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
COMPOSE_TEST="$COMPOSE_DIR/docker-compose.test.yml"
E2E_DIR="$PROJECT_ROOT/test/e2e"

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

# ── Step 0: Start environment ──

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

# ── Step 1: Update test channel to point to mock upstream ──

log "Updating test channel to point to mock-upstream..."
docker exec mysql mysql -uroot -proot123456 oneapi -e \
    "UPDATE channels SET base_url='http://mock-upstream:9999', \`key\`='sk-mock-key' WHERE id=1;" 2>/dev/null

# Verify update
channel_url=$(docker exec mysql mysql -uroot -proot123456 oneapi -N -e \
    "SELECT base_url FROM channels WHERE id=1;" 2>/dev/null)
if [ "$channel_url" = "http://mock-upstream:9999" ]; then
    log "Channel updated: $channel_url"
else
    fail "Channel update failed: got '$channel_url'"
    exit 1
fi

# ── Step 2: Register test user and set quota ──

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
docker exec mysql mysql -uroot -proot123456 oneapi -e \
    "UPDATE users SET quota=1000000 WHERE id=$E2E_USER_ID;" 2>/dev/null
log "Quota set to 1000000"

# ── Step 3: Run full E2E test ──

log "Running full E2E test..."
"$E2E_DIR/e2e-test" --step all
E2E_EXIT=$?

# Cleanup binary
rm -f "$E2E_DIR/e2e-test"

exit $E2E_EXIT
