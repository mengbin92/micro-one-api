#!/usr/bin/env bash
# v0.21 P1: execute real MySQL/Postgres migrations against a scratch database.
#
# The caller is responsible for providing a healthy service container (CI does
# this with services:). By default the script performs:
#   1. fresh apply       — every migration must execute successfully;
#   2. repeat apply      — must print exactly "nothing to apply";
#   3. status audit      — every listed migration must be recorded as applied;
#   4. negative gate     — a deliberately invalid pending migration must fail
#                          before its version can be recorded as applied.
#
# Usage:
#   scripts/test-migration-smoke.sh mysql
#   scripts/test-migration-smoke.sh postgres
#
# DSN defaults match CI service containers. Override MIGRATIONS_DSN to point
# at a local scratch database.

set -euo pipefail

dialect="${1:-}"
case "$dialect" in
  mysql)
    dir="./migrations"
    default_dsn="root:migration_smoke@tcp(127.0.0.1:3306)/migration_smoke?multiStatements=true"
    ;;
  postgres)
    dir="./migrations/postgres"
    default_dsn="postgres://migration_smoke:migration_smoke@127.0.0.1:5432/migration_smoke?sslmode=disable"
    ;;
  *)
    echo "usage: $0 mysql|postgres" >&2
    exit 2
    ;;
esac

dsn="${MIGRATIONS_DSN:-$default_dsn}"
export MIGRATIONS_DSN="$dsn"
export MIGRATIONS_DRIVER="$dialect"

# Resolve repo root even when invoked through make from a nested directory.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

run_migrate() {
  go run ./cmd/migrate -dir "$dir" "$@"
}

wait_for_database() {
  # cmd/migrate pings before doing anything else. Retry here so service-container
  # startup races produce a clear readiness error instead of a flaky smoke run.
  local attempt
  for attempt in $(seq 1 30); do
    if run_migrate -status >/dev/null 2>&1; then
      echo "$dialect database is ready"
      return 0
    fi
    sleep 2
  done
  echo "error: $dialect database was not ready after 60s" >&2
  run_migrate -status >&2 || true
  return 1
}

expected_count() {
  # Count SQL files after applying the runner's intentional exclusions. The
  # negative fixture is added only in its own temporary directory.
  find "$dir" -maxdepth 1 -type f -name '*.sql' \
    ! -name 'phase3_partitioning.sql' \
    ! -name 'schema_split.sql' \
    | wc -l | tr -d ' '
}

assert_status_applied() {
  local status_output="$1" expected="$2" applied
  applied="$(printf '%s\n' "$status_output" | awk '$2 == "yes" { count++ } END { print count + 0 }')"
  if [ "$applied" != "$expected" ]; then
    echo "error: expected $expected applied $dialect migrations, got $applied" >&2
    printf '%s\n' "$status_output" >&2
    exit 1
  fi
}

wait_for_database
expected="$(expected_count)"
echo "== $dialect migration smoke: expecting $expected migrations =="

echo "== fresh apply =="
fresh_output="$(run_migrate)"
printf '%s\n' "$fresh_output"
applied_count="$(printf '%s\n' "$fresh_output" | grep -c '^  - ' || true)"
if [ "$applied_count" != "$expected" ]; then
  echo "error: fresh apply executed $applied_count migrations, expected $expected" >&2
  exit 1
fi

echo "== repeat apply (idempotence) =="
repeat_output="$(run_migrate)"
printf '%s\n' "$repeat_output"
if [ "$repeat_output" != "nothing to apply" ]; then
  echo "error: repeat apply was not a no-op" >&2
  exit 1
fi

echo "== migration status audit =="
status_output="$(run_migrate -status)"
printf '%s\n' "$status_output"
assert_status_applied "$status_output" "$expected"

echo "== negative failure injection =="
negative_dir="$(mktemp -d "${TMPDIR:-/tmp}/migration-smoke-${dialect}.XXXXXX")"
trap 'rm -rf "$negative_dir"' EXIT
find "$dir" -maxdepth 1 -type f -name '*.sql' -exec ln -s "$(pwd)/{}" "$negative_dir/" \;
cat > "$negative_dir/999_migration_smoke_failure_injection.sql" <<'SQL'
-- Deliberately invalid statement: the smoke gate must fail and roll back.
CREATE "TABLE" migration_smoke_failure_injection (id BIGINT);
SQL

negative_output="$(go run ./cmd/migrate -dir "$negative_dir" 2>&1 || true)"
printf '%s\n' "$negative_output"
if ! printf '%s\n' "$negative_output" | grep -q 'apply 999_migration_smoke_failure_injection'; then
  echo "error: invalid migration did not surface the failing version" >&2
  exit 1
fi
if printf '%s\n' "$negative_output" | grep -q '^applied 1 migration'; then
  echo "error: invalid migration was reported as applied" >&2
  exit 1
fi

echo "== $dialect migration smoke passed =="
