#!/usr/bin/env bash
# Run only on an independent native runner. No deployment settings are read.
set -euo pipefail

driver="${1:?sqlite, mysql or postgres required}"
case "$driver" in sqlite|mysql|postgres) ;; *) exit 2 ;; esac
test "$(uname -s)/$(uname -m)" = Linux/x86_64
repo_root="$(git rev-parse --show-toplevel)"
results="${TIMING_SQL_RESULTS:?temporary result directory required}"
baseline_ref="${TIMING_SQL_BASELINE:?baseline revision required}"
mkdir -p "$results"
results="$(cd "$results" && pwd)"
scratch="$(mktemp -d)"
baseline="$scratch/baseline"
measurement="timing-capacity-measure-$driver"
database="timing-capacity-database-$driver"
cache="timing-capacity-build-$driver"
created_measurement=false
created_database=false
created_cache=false
created_worktree=false

cleanup_containers() {
  if "$created_database"; then docker rm -f "$database" >/dev/null; created_database=false; fi
  if "$created_measurement"; then docker rm -f "$measurement" >/dev/null; created_measurement=false; fi
}
cleanup() {
  cleanup_containers
  if "$created_cache"; then docker volume rm "$cache" >/dev/null || true; fi
  if "$created_worktree"; then git worktree remove --force "$baseline"; fi
  rm -rf "$scratch"
}
trap cleanup EXIT

git worktree add --detach "$baseline" "$baseline_ref"
created_worktree=true
# The same measurement code drives both production revisions. Baseline keeps
# its original SQL construction/backpressure tests and original worker count.
cp "$repo_root/internal/server/request_timing_perf_test.go" "$baseline/internal/server/"
cp "$repo_root/internal/server/request_timing_sql_diagnostic_test.go" "$baseline/internal/server/"
{
  git rev-parse HEAD
  git rev-parse "$baseline_ref"
  uname -a
  lscpu
  free -m
  go version
  docker version --format '{{.Server.Version}}'
  sha256sum "$repo_root/internal/server/request_timing_perf_test.go" "$repo_root/internal/server/request_timing_sql_diagnostic_test.go"
} > "$results/environment.txt"

# Generated stubs are intentionally ignored by Git. Generate in both source
# trees rather than copying or hand-editing protobuf outputs.
make init
export PATH="$(go env GOPATH)/bin:$PATH"
make proto
(cd "$baseline" && make proto)
go mod download
docker pull golang:1.27.2-bookworm
case "$driver" in
  mysql) docker pull mysql:8.0 ;;
  postgres) docker pull postgres:18 ;;
esac
docker volume create "$cache" >/dev/null
created_cache=true

build_binary() {
  local label="$1" checkout="$2"
  docker run --rm --network none --cpus=4 --memory=4g \
    -e GOFLAGS=-buildvcs=false \
    -v "$checkout:$checkout:ro" -w "$checkout" \
    -v "$(go env GOMODCACHE):/go/pkg/mod:ro" -v "$cache:/root/.cache/go-build" \
    -v "$results:$results" golang:1.27.2-bookworm \
    go test -c -o "$results/server-$label.test" ./internal/server \
    > "$results/build-$label.log" 2>&1
}
build_binary baseline "$baseline"
build_binary candidate "$repo_root"
for package in app/log/internal/data internal/server domain/requesttrace; do
  binary="$(basename "$package")"
  docker run --rm --network none --cpus=4 --memory=4g \
    -e GOFLAGS=-buildvcs=false -v "$repo_root:$repo_root:ro" -w "$repo_root" \
    -v "$(go env GOMODCACHE):/go/pkg/mod:ro" -v "$cache:/root/.cache/go-build" \
    -v "$results:$results" golang:1.27.2-bookworm \
    go test -race -c -o "$results/$binary-race.test" "./$package" \
    > "$results/build-$binary-race.log" 2>&1
done

db_args=()
case "$driver" in
  mysql) db_args=(-e 'GROUP_CONTEXT_TEST_MYSQL_DSN=root@tcp(127.0.0.1:3306)/mysql?parseTime=true&multiStatements=true') ;;
  postgres) db_args=(-e 'GROUP_CONTEXT_TEST_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable') ;;
esac

run_case() {
  local label="$1" concurrency="$2" mode="$3" test_name rc
  test_name=TestRequestTimingSQLOverhead
  if [[ "$mode" == diagnostic ]]; then test_name=TestRequestTimingSQLDiagnosticOverhead; fi
  set +e
  docker exec -e GOMAXPROCS=4 -e TIMING_SQL_ACCEPTANCE=1 \
    -e TIMING_SQL_DIAGNOSTIC_BENCH=1 -e TIMING_SQL_DRIVER="$driver" \
    -e TIMING_SQL_CONCURRENCY="$concurrency" \
    -e "TIMING_PERF_OUTPUT=$results/$label-$driver-$concurrency-$mode.json" \
    "${db_args[@]}" \
    "$measurement" "$results/server-$label.test" \
    -test.run "^$test_name\$" -test.v -test.timeout=7m \
    > "$results/$label-$driver-$concurrency-$mode.log" 2>&1
  rc=$?
  set -e
  printf '%s %s %s %s %d\n' "$label" "$driver" "$concurrency" "$mode" "$rc" >> "$results/exit-status.txt"
  tail -n 5 "$results/$label-$driver-$concurrency-$mode.log"
  # Baseline failure is evidence, not a reason to omit the candidate. Every
  # candidate gate retains its exit code and contributes to the final verdict.
  if [[ "$label" == candidate && "$rc" != 0 ]]; then candidate_failed=1; fi
}

candidate_failed=0
for label in baseline candidate; do
  checkout="$repo_root"
  if [[ "$label" == baseline ]]; then checkout="$baseline"; fi
  docker run -d --name "$measurement" --network none --cpus=4 --memory=1g \
    -v "$checkout:$checkout:ro" -w "$checkout" -v "$results:$results" \
    golang:1.27.2-bookworm sleep infinity >/dev/null
  created_measurement=true
  if [[ "$driver" == mysql ]]; then
    docker run -d --name "$database" --network "container:$measurement" --cpus=1 --memory=1g \
      -e MYSQL_ALLOW_EMPTY_PASSWORD=yes mysql:8.0 --bind-address=127.0.0.1 >/dev/null
    created_database=true
    for attempt in {1..60}; do
      if docker exec "$database" mysqladmin ping --silent >/dev/null 2>&1; then break; fi
      sleep 2
    done
    docker exec "$database" mysqladmin ping --silent
  elif [[ "$driver" == postgres ]]; then
    docker run -d --name "$database" --network "container:$measurement" --cpus=1 --memory=512m \
      -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18 -c listen_addresses=127.0.0.1 >/dev/null
    created_database=true
    for attempt in {1..60}; do
      if docker exec "$database" pg_isready -U postgres >/dev/null 2>&1; then break; fi
      sleep 2
    done
    docker exec "$database" pg_isready -U postgres
  fi
  for concurrency in 16 32; do run_case "$label" "$concurrency" diagnostic; done
  run_case "$label" 64 acceptance
  docker inspect "$measurement" --format '{{json .State}}' > "$results/$label-container-state.json"
  docker inspect "$measurement" --format '{{json .HostConfig}}' > "$results/$label-container-limits.json"
  docker image inspect golang:1.27.2-bookworm --format '{{.Id}} {{.Architecture}}' > "$results/image.txt"
  sha256sum "$results/server-$label.test" > "$results/$label-binary.sha256"
  if [[ "$label" == candidate ]]; then
    for binary in data server requesttrace; do
      pattern='TestTiming|TestExporter|TestRequestTimingSQL(FailureRecovery|BackpressureAndLostAck)$'
      if [[ "$binary" == requesttrace ]]; then pattern='.'; fi
      set +e
      docker exec -e GOMAXPROCS=4 "${db_args[@]}" "$measurement" \
        "$results/$binary-race.test" -test.run "$pattern" -test.count=10 -test.v -test.timeout=5m \
        > "$results/$binary-race.log" 2>&1
      rc=$?
      set -e
      printf 'candidate %s %s-race %d\n' "$driver" "$binary" "$rc" >> "$results/exit-status.txt"
      if [[ "$rc" != 0 ]]; then candidate_failed=1; fi
      tail -n 5 "$results/$binary-race.log"
    done
  fi
  cleanup_containers
done
# Binaries and compiler logs can be large; source revision and binary hashes
# remain in the artifact together with all successful and failed raw results.
rm -f "$results/"*.test
exit "$candidate_failed"
