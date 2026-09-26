#!/usr/bin/env bash
# Compare ff518b1 with the checked-out commit on one Linux/amd64 CI runner.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
baseline_ref="${Q3_BASELINE_REF:-ff518b1}"
results_dir="${Q3_RESULTS_DIR:-${RUNNER_TEMP:-/tmp}/q3-benchmark-results}"
baseline_dir="${RUNNER_TEMP:-/tmp}/q3-baseline-worktree"
compose_overlay="$repo_root/scripts/benchmark/q3-compose.yml"
compose_project=q3benchmark
active_checkout=""
created_env_files=()
created_token_files=()

if [[ "$(uname -s)/$(uname -m)" != "Linux/x86_64" ]]; then
  echo 'Q3 comparison requires Linux/amd64' >&2
  exit 2
fi
if [[ -e "$repo_root/deployments/docker-compose/.env" ]]; then
  echo 'Refusing to run with an existing deployments/docker-compose/.env' >&2
  exit 2
fi

mkdir -p "$results_dir"
results_dir="$(cd "$results_dir" && pwd)"
chmod 700 "$results_dir"

cleanup_stack() {
  if [[ -n "$active_checkout" ]]; then
    docker compose -p "$compose_project" \
      -f "$active_checkout/deployments/docker-compose/docker-compose.yml" \
      -f "$compose_overlay" \
      --env-file "$active_checkout/deployments/docker-compose/.env" \
      down -v --remove-orphans > /dev/null 2>&1 || true
    active_checkout=""
  fi
  if (( ${#created_token_files[@]} > 0 )); then
    rm -f "${created_token_files[@]}"
  fi
  if (( ${#created_env_files[@]} > 0 )); then
    rm -f "${created_env_files[@]}"
  fi
}
trap cleanup_stack EXIT

git -C "$repo_root" worktree add --detach "$baseline_dir" "$baseline_ref"
baseline_sha="$(git -C "$baseline_dir" rev-parse HEAD)"
candidate_sha="$(git -C "$repo_root" rev-parse HEAD)"

{
  printf 'baseline_sha=%s\ncandidate_sha=%s\n' "$baseline_sha" "$candidate_sha"
  printf 'harness_sha=%s\n' "$(sha256sum "$repo_root/scripts/benchmark/k6-baseline.js" | cut -d' ' -f1)"
  printf 'mock_sha=%s\n' "$(sha256sum "$repo_root/scripts/benchmark/mock-upstream/main.go" | cut -d' ' -f1)"
  uname -a
  lscpu
  free -h
  docker version --format 'docker_server={{.Server.Version}}'
  docker compose version
  docker run --rm grafana/k6:0.54.0 version
} > "$results_dir/environment.txt"

docker build -q -f "$repo_root/scripts/benchmark/Dockerfile.mock-upstream" \
  -t q3-mock-upstream:local "$repo_root" > /dev/null

root_password="$(openssl rand -hex 16)"
redis_password="$(openssl rand -hex 16)"
admin_token="$(openssl rand -hex 24)"
service_token="$(openssl rand -hex 24)"
jwt_secret="$(openssl rand -hex 32)"
channel_key="$(openssl rand -hex 16)"
user_password="$(openssl rand -hex 16)"

write_environment() {
  local checkout="$1"
  local env_file="$checkout/deployments/docker-compose/.env"
  if [[ -e "$env_file" ]]; then
    echo "Refusing to overwrite existing $env_file" >&2
    exit 2
  fi
  mkdir -p "$checkout/cert/alipay" "$checkout/web/dist"
  umask 077
  created_env_files+=("$env_file")
  cat > "$env_file" <<EOF
MYSQL_ROOT_PASSWORD=$root_password
DATABASE_DSN=root:$root_password@tcp(mysql:3306)/oneapi?charset=utf8mb4&parseTime=True&loc=Local
REDIS_PASSWORD=$redis_password
JWT_SECRET_KEY=$jwt_secret
CHANNEL_ENCRYPTION_KEY=$channel_key
SERVICE_TOKEN=$service_token
ADMIN_TOKEN=$admin_token
GRAFANA_ADMIN_PASSWORD=$admin_token
INITIAL_ADMIN_USERNAME=admin
INITIAL_ADMIN_EMAIL=admin@example.invalid
INITIAL_ADMIN_PASSWORD=$user_password
ALIPAY_ENABLED=false
ALIPAY_CERT_DIR=$checkout/cert/alipay
CHANNEL_HEALTH_ALERT_ENABLED=false
RECON_ALERT_ENABLED=false
EOF
}

reset_samples() {
  local tables=(billing_ledger_dedupe_claims billing_request_snapshots billing_pricing_snapshots billing_settlement_tasks billing_reservations billing_ledgers logs)
  local table sql='SET FOREIGN_KEY_CHECKS=0;'
  for table in "${tables[@]}"; do
    if docker exec -e MYSQL_PWD="$root_password" mysql \
      mysql -uroot -N oneapi -e "SHOW TABLES LIKE '$table'" | grep -qx "$table"; then
      sql+="DELETE FROM \`$table\`;"
    fi
  done
  sql+='SET FOREIGN_KEY_CHECKS=1;'
  docker exec -e MYSQL_PWD="$root_password" mysql mysql -uroot oneapi -e "$sql"
}

run_version() {
  local label="$1" checkout="$2" run
  local compose_file="$checkout/deployments/docker-compose/docker-compose.yml"
  local compose_env="$checkout/deployments/docker-compose/.env"
  local token_env="$checkout/q3-token.env"
  write_environment "$checkout"
  active_checkout="$checkout"
  echo "Building and starting $label ($(git -C "$checkout" rev-parse --short HEAD))"
  if ! COMPOSE_PARALLEL_LIMIT=2 docker compose -p "$compose_project" \
    -f "$compose_file" -f "$compose_overlay" --env-file "$compose_env" \
    up -d --build admin-api relay-gateway mock-upstream \
    > "$results_dir/$label-compose-build.log" 2>&1; then
    tail -n 100 "$results_dir/$label-compose-build.log" >&2
    exit 1
  fi
  created_token_files+=("$token_env")
  Q3_USER_PASSWORD="$user_password" Q3_ADMIN_TOKEN="$admin_token" \
    python3 "$repo_root/scripts/benchmark/q3-fixture.py" "$token_env"
  reset_samples
  for run in 1 2 3; do
    echo "Running $label full profile $run/3"
    docker run --rm --network host --user "$(id -u):$(id -g)" \
      --env-file "$token_env" \
      -e BASE_URL=http://127.0.0.1:8080 \
      -e ITERATION_TARGET_RATE=10 -e ITERATION_START_RATE=10 \
      -e RAMP_HOLD=2m -e PREALLOCATED_VUS=100 -e MAX_VUS=1000 \
      -e RESULTS_FILE="/results/summary-$label-$run.json" \
      -v "$repo_root/scripts/benchmark:/benchmark:ro" \
      -v "$results_dir:/results" \
      grafana/k6:0.54.0 run \
      --out "json=/results/raw-$label-$run.json" \
      /benchmark/k6-baseline.js \
      > "$results_dir/output-$label-$run.txt" 2>&1 || {
        tail -n 100 "$results_dir/output-$label-$run.txt" >&2
        exit 1
      }
    reset_samples
  done
  rm -f "$token_env"
  docker compose -p "$compose_project" -f "$compose_file" -f "$compose_overlay" \
    --env-file "$compose_env" down -v --remove-orphans --rmi local > /dev/null
  active_checkout=""
  rm -f "$compose_env"
}

run_version baseline "$baseline_dir"
run_version candidate "$repo_root"

comparison_args=()
for run in 1 2 3; do
  comparison_args+=(--baseline "$results_dir/summary-baseline-$run.json")
  comparison_args+=(--candidate "$results_dir/summary-candidate-$run.json")
done
python3 "$repo_root/scripts/benchmark/summarize-regression.py" \
  "${comparison_args[@]}" --output "$results_dir/regression-report.json" \
  | tee "$results_dir/regression-output.txt"
