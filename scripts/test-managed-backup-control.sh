#!/usr/bin/env bash
# Own disposable local databases only; no deployment .env or DSN is read.
set -euo pipefail
cd "$(dirname "$0")/.."
B3_CONTROL_SCRATCH="$(mktemp -d /tmp/b3-control.XXXXXX)"
B3_CONTROL_PROJECT="b3-control-$(openssl rand -hex 4)"
export B3_CONTROL_PASSWORD="$(openssl rand -hex 16)"
chmod 700 "$B3_CONTROL_SCRATCH"
cat > "$B3_CONTROL_SCRATCH/compose.json" <<'JSON'
{"services":{"mysql":{"image":"mysql:8.4","environment":{"MYSQL_ROOT_PASSWORD":"${B3_CONTROL_PASSWORD}"},"ports":["127.0.0.1::3306"],"healthcheck":{"test":["CMD-SHELL","MYSQL_PWD=\"$$MYSQL_ROOT_PASSWORD\" mysqladmin ping -h 127.0.0.1 -uroot --silent"],"interval":"2s","timeout":"3s","retries":60}},"postgres":{"image":"postgres:18","environment":{"POSTGRES_PASSWORD":"${B3_CONTROL_PASSWORD}"},"ports":["127.0.0.1::5432"],"healthcheck":{"test":["CMD-SHELL","pg_isready -U postgres"],"interval":"2s","timeout":"3s","retries":60}}}}
JSON
b3_compose(){ docker compose -f "$B3_CONTROL_SCRATCH/compose.json" -p "$B3_CONTROL_PROJECT" "$@"; }
b3_cleanup(){ b3_compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$B3_CONTROL_SCRATCH"; }
trap b3_cleanup EXIT
b3_compose up -d --wait --wait-timeout 150
B3_MYSQL_ADDR="$(b3_compose port mysql 3306)"
B3_POSTGRES_ADDR="$(b3_compose port postgres 5432)"
export GROUP_CONTEXT_TEST_MYSQL_DSN="root:${B3_CONTROL_PASSWORD}@tcp(${B3_MYSQL_ADDR})/mysql?parseTime=true&multiStatements=true"
export GROUP_CONTEXT_TEST_POSTGRES_DSN="postgres://postgres:${B3_CONTROL_PASSWORD}@${B3_POSTGRES_ADDR}/postgres?sslmode=disable"
go test ./internal/integration -run '^TestManagedBackupAdminHTTPAndRPC$' -count=1 -v
go test ./app/identity/internal/data -run '^TestManagedBackupStepUpIsSessionBoundInLegacyAndIAM$' -count=1 -v
