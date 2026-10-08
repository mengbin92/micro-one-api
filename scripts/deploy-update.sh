#!/bin/bash
# Cross-platform build + deploy for production.
# Usage: scripts/deploy-update.sh [services...]
#   No arguments -> deploys the historical default pair (billing-service, admin-api).
#   With arguments -> deploys exactly the given services, e.g.
#     scripts/deploy-update.sh identity-service channel-service billing-service admin-api relay-gateway
# Each run tags the previously-live images with one shared rollback-<timestamp>
# tag before loading the new build.

set -euo pipefail

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }

# Get project root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="${SCRIPT_DIR}/.."

# Only deployment coordinates are read; .env is data, never executable code.
if [ -f "${PROJECT_ROOT}/.env" ]; then
    while IFS='=' read -r key value; do
        case "$key" in
            DEPLOY_REMOTE_SERVER|DEPLOY_REMOTE_DIR)
                value="${value%\"}"; value="${value#\"}"
                value="${value%\'}"; value="${value#\'}"
                if [ -z "${!key:-}" ]; then export "$key=$value"; fi ;;
        esac
    done < "${PROJECT_ROOT}/.env"
fi
: "${DEPLOY_REMOTE_SERVER:?DEPLOY_REMOTE_SERVER is required}"
: "${DEPLOY_REMOTE_DIR:?DEPLOY_REMOTE_DIR is required}"
# SSH passes remote arguments through a shell. Reject shell syntax in these
# two coordinates rather than interpolating untrusted executable text.
[[ "$DEPLOY_REMOTE_SERVER" =~ ^[a-zA-Z0-9_@.:-]+$ && "$DEPLOY_REMOTE_SERVER" != -* ]]
[[ "$DEPLOY_REMOTE_DIR" =~ ^/[a-zA-Z0-9_./-]+$ ]]
SERVER="$DEPLOY_REMOTE_SERVER"
COMPOSE_DIR="${DEPLOY_REMOTE_DIR}/docker-compose"

log_info "======================================"
log_info "  Micro-One-API Production Deploy"
log_info "======================================"
log_info "Services: ${*:-(default: billing-service admin-api)}"
log_info "Server: ${SERVER}"
log_info "Project root: ${PROJECT_ROOT}"
echo ""

# Map service name to build path
service_path() {
    case "$1" in
        relay-gateway)      echo "./cmd/relay-gateway" ;;
        admin-api)          echo "./app/admin/cmd/admin" ;;
        identity-service)   echo "./app/identity/cmd/identity" ;;
        channel-service)    echo "./app/channel/cmd/channel" ;;
        billing-service)    echo "./app/billing/cmd/billing" ;;
        config-service)     echo "./app/config/cmd/config" ;;
        log-service)        echo "./app/log/cmd/log" ;;
        monitor-worker)     echo "./app/monitor/cmd/monitor" ;;
        notify-worker)      echo "./app/notify/cmd/notify" ;;
        *)                  echo "" ;;
    esac
}

service_dockerfile() {
    case "${1}" in
        relay-gateway)      echo "Dockerfile" ;;
        admin-api)          echo "app/admin/Dockerfile" ;;
        identity-service)   echo "app/identity/Dockerfile" ;;
        channel-service)    echo "app/channel/Dockerfile" ;;
        billing-service)    echo "app/billing/Dockerfile" ;;
        config-service)     echo "app/config/Dockerfile" ;;
        log-service)        echo "app/log/Dockerfile" ;;
        monitor-worker)     echo "app/monitor/Dockerfile" ;;
        notify-worker)      echo "app/notify/Dockerfile" ;;
        *)                  echo "Unknown service: ${1}" >&2; exit 1 ;;
    esac
}

# Deploy services. Accepts an explicit service list (see service_path for the
# valid names); falls back to the historical billing+admin pair when no
# arguments are given.
if [ "$#" -gt 0 ]; then
    SERVICES=("$@")
else
    SERVICES=(billing-service admin-api)
fi

# Validate the entire list before any build or remote call. A typo in a later
# argument must not leave an earlier service already deployed.
for svc in "${SERVICES[@]}"; do
    if [ -z "$(service_path "$svc")" ]; then
        log_error "Unknown service: ${svc}"
        exit 1
    fi
done

# Check prerequisites
log_step "Checking prerequisites..."
if ! docker buildx version &>/dev/null; then
    log_error "docker buildx not available"
    exit 1
fi

ssh -o BatchMode=yes -o ConnectTimeout=10 "$SERVER" bash -s -- "$COMPOSE_DIR" "${SERVICES[@]}" <<'REMOTE'
set -euo pipefail
cd "$1"; shift
docker compose config --quiet
for service in "$@"; do
    cid=$(docker compose ps -q "$service")
    [[ -n "$cid" && "$cid" != *$'\n'* ]]
    test "$(docker inspect "$cid" --format '{{.State.Running}}')" = true
done
REMOTE
log_info "Prerequisites OK"
echo ""

# Rollback tag shared by every service deployed in this run, so a multi-service
# deploy can be rolled back to one consistent point in time.
ROLLBACK_TAG="rollback-$(date +%Y%m%d-%H%M%S)"
DEPLOY_TAG="deploy-$(date +%Y%m%d-%H%M%S)-$(git -C "$PROJECT_ROOT" rev-parse --short HEAD)"
SOURCE_DIGEST=$(python3 "${PROJECT_ROOT}/scripts/rbac-source-digest.py")
[[ "$SOURCE_DIGEST" =~ ^[a-f0-9]{64}$ ]]

# Function to build and deploy a service
deploy_service() {
    local service=$1
    local image_name="docker-compose-${service}:${DEPLOY_TAG}"
    local temp_file
    temp_file=$(mktemp "/tmp/${service}-image.tar.gz.XXXXXX")

    log_step "========================================"
    log_step "Building ${service} (linux/amd64)..."
    log_step "========================================"

    # Build image (cross-platform)
    (cd "$PROJECT_ROOT" && docker buildx build \
        --platform linux/amd64 \
        --load \
        --progress=plain \
        --label "micro-one-api.source.digest=${SOURCE_DIGEST}" \
        -f "$(service_dockerfile "$service")" \
        --build-arg "SERVICE_NAME=${service}" --build-arg "SERVICE_PATH=$(service_path "$service")" \
        -t "$image_name" \
        .)

    # Get image size
    local expected_id
    expected_id=$(docker inspect "$image_name" --format '{{.Id}}')
    [[ "$expected_id" =~ ^sha256:[a-f0-9]{64}$ ]]

    # Save image (gzipped: the upload link is the bottleneck, docker load
    # accepts .tar.gz directly)
    log_info "Saving ${service} image..."
    docker save "$image_name" | gzip > "$temp_file"

    # Transfer to server
    log_info "Uploading to server..."
    scp "$temp_file" "$SERVER:/tmp/"

    # Load and deploy on server
    log_info "Deploying on server (rollback tag: ${ROLLBACK_TAG})..."
    ssh "$SERVER" bash -s -- "$COMPOSE_DIR" "$service" "$image_name" "$expected_id" "$ROLLBACK_TAG" "${temp_file##*/}" <<'REMOTE'
set -euo pipefail
cd "$1"
service=$2; image=$3; expected=$4; rollback=$5; archive="/tmp/$6"
cid=$(docker compose ps -q "$service")
[[ -n "$cid" && "$cid" != *$'\n'* ]]
old=$(docker inspect "$cid" --format '{{.Image}}')
docker tag "$old" "docker-compose-${service}:${rollback}"
docker load -i "$archive"
test "$(docker inspect "$image" --format '{{.Id}}')" = "$expected"

# Pin the selected service in the default Compose file, so later ordinary
# compose invocations use this build too. Preserve all other YAML verbatim.
file=""
for candidate in compose.yaml compose.yml docker-compose.yaml docker-compose.yml; do
    if [ -f "$candidate" ]; then file=$candidate; break; fi
done
test -n "$file"
if [ ! -e "$file.$rollback" ]; then cp -p "$file" "$file.$rollback"; fi
previous=$(mktemp "${file}.before-image.XXXXXX")
cp -p "$file" "$previous"
python3 - "$file" "$service" "$image" <<'PY'
import os, re, sys, tempfile
from pathlib import Path
path, service, image = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
text = path.read_text()
blocks = list(re.finditer(r'^  '+re.escape(service)+r':\s*\n(?:^(?:    .*|\s*|#.*)\n)*', text, re.M))
if len(blocks) != 1:
    raise SystemExit('Expected one standard service block; Compose file unchanged')
block = blocks[0]
body = block.group()
pattern = r'^    image:.*$'
if len(re.findall(pattern, body, re.M)) > 1:
    raise SystemExit('Ambiguous image field; Compose file unchanged')
if re.search(pattern, body, re.M):
    body = re.sub(pattern, '    image: '+image, body, flags=re.M)
else:
    header, rest = body.split('\n', 1)
    body = header+'\n    image: '+image+'\n'+rest
fd, name = tempfile.mkstemp(dir=path.parent)
with os.fdopen(fd, 'w') as out:
    out.write(text[:block.start()]+body+text[block.end():])
os.chmod(name, path.stat().st_mode & 0o777)
os.replace(name, path)
PY
# Compose includes dependency images even when a service is specified. Read
# the selected service explicitly from the resolved configuration instead.
if ! test "$(docker compose config --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["services"][sys.argv[1]]["image"])' "$service")" = "$image"; then
    cp -p "$previous" "$file"
    rm -f "$previous"
    echo 'Effective Compose image differs; restored configuration' >&2
    exit 1
fi
rm -f "$previous"
docker compose up -d --no-deps --no-build --pull never "$service"
for attempt in {1..30}; do
    cid=$(docker compose ps -q "$service")
    test -n "$cid"
    test "$(docker inspect "$cid" --format '{{.Image}}')" = "$expected"
    state=$(docker inspect "$cid" --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')
    case "$state" in
        'running healthy'|'running none') echo "$service image=$expected state=$state rollback=$rollback"; break ;;
        *unhealthy*|exited*|dead*) echo "$service failed: $state" >&2; exit 1 ;;
    esac
    test "$attempt" -lt 30
    sleep 2
done
rm -f "$archive"
REMOTE

    # Cleanup local temp file
    rm -f "$temp_file"

    log_info "${service} deployed successfully!"
    echo ""
}

for svc in "${SERVICES[@]}"; do
    deploy_service "${svc}"
done

log_info "Migrations and frontend assets use their separate release steps."
log_info "========================================"
log_info "Deployment completed!"
log_info "========================================"
