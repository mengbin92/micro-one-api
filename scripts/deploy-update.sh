#!/bin/bash
# Cross-platform build + deploy for production.
# Usage: scripts/deploy-update.sh [services...]
#   No arguments -> deploys the historical default pair (billing-service, admin-api).
#   With arguments -> deploys exactly the given services, e.g.
#     scripts/deploy-update.sh identity-service channel-service billing-service admin-api relay-gateway
# Each run tags the previously-live images with one shared rollback-<timestamp>
# tag before loading the new build.

set -e

SERVER="root@43.133.65.212"
SERVER_DIR="/opt/micro-one-api"
COMPOSE_DIR="${SERVER_DIR}/docker-compose"

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

log_info "======================================"
log_info "  Micro-One-API Production Deploy"
log_info "======================================"
log_info "Services: ${*:-(default: billing-service admin-api)}"
log_info "Server: ${SERVER}"
log_info "Project root: ${PROJECT_ROOT}"
echo ""

# Check prerequisites
log_step "Checking prerequisites..."
if ! docker buildx version &>/dev/null; then
    log_error "docker buildx not available"
    exit 1
fi

if ! ssh -o ConnectTimeout=5 ${SERVER} "echo 'Connected'" &>/dev/null; then
    log_error "Cannot connect to server ${SERVER}"
    exit 1
fi
log_info "Prerequisites OK"
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

# Rollback tag shared by every service deployed in this run, so a multi-service
# deploy can be rolled back to one consistent point in time.
ROLLBACK_TAG="rollback-$(date +%Y%m%d-%H%M%S)"

# Function to build and deploy a service
deploy_service() {
    local service=$1
    local image_name="docker-compose-${service}:latest"
    local temp_file="/tmp/${service}-image.tar.gz"

    log_step "========================================"
    log_step "Building ${service} (linux/amd64)..."
    log_step "========================================"

    # Build image (cross-platform)
    (cd ${PROJECT_ROOT} && docker buildx build \
        --platform linux/amd64 \
        --load \
        --progress=plain \
        -f $(service_dockerfile ${service}) \
        --build-arg SERVICE_NAME=${service} --build-arg SERVICE_PATH=$(service_path ${service}) \
        -t ${image_name} \
        .)

    # Get image size
    local size=$(docker inspect ${image_name} --format='{{.Size}}' | awk '{printf "%.2f MB", $1/1024/1024}')
    log_info "Built image size: ${size}"

    # Save image (gzipped: the upload link is the bottleneck, docker load
    # accepts .tar.gz directly)
    log_info "Saving ${service} image..."
    docker save ${image_name} | gzip > ${temp_file}

    # Transfer to server
    log_info "Uploading to server..."
    scp ${temp_file} ${SERVER}:/tmp/

    # Load and deploy on server
    log_info "Deploying on server (rollback tag: ${ROLLBACK_TAG})..."
    ssh ${SERVER} bash << EOF
        set -e

        echo "Tagging current image for rollback..."
        docker tag ${image_name} docker-compose-${service}:${ROLLBACK_TAG} 2>/dev/null || true

        echo "Loading image..."
        docker load -i /tmp/${service}-image.tar.gz

        echo "Updating container via docker compose..."
        cd ${COMPOSE_DIR}

        # Restart service with docker compose
        docker compose up -d --no-deps ${service}

        # Cleanup
        rm -f /tmp/${service}-image.tar.gz

        echo "Deployed ${service}!"
EOF

    # Cleanup local temp file
    rm -f ${temp_file}

    log_info "${service} deployed successfully!"
    echo ""
}

# Deploy services. Accepts an explicit service list (see service_path for the
# valid names); falls back to the historical billing+admin pair when no
# arguments are given.
if [ "$#" -gt 0 ]; then
    SERVICES=("$@")
else
    SERVICES=(billing-service admin-api)
fi

for svc in "${SERVICES[@]}"; do
    deploy_service "${svc}"
done

# Apply database migrations
log_step "Applying database migrations..."
ssh ${SERVER} bash << 'EOF'
    # Check if there are new migration files not yet applied
    MIGRATION_DIR="/opt/micro-one-api/migrations"
    LATEST_FILE=$(ls -t ${MIGRATION_DIR}/*.sql 2>/dev/null | head -1)

    if [ -n "$LATEST_FILE" ]; then
        echo "Latest migration: $(basename ${LATEST_FILE})"
        echo "Please verify migrations are applied correctly"
    fi
EOF
echo ""

# Check status
log_step "Checking service status..."
ssh ${SERVER} bash << 'EOF'
    echo "======================================"
    echo "  Container Status"
    echo "======================================"
    docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}' | grep -E 'NAME|billing-service|admin-api|relay-gateway'

    echo ""
    echo "======================================"
    echo "  Recent Logs (billing-service)"
    echo "======================================"
    docker logs --tail 20 billing-service 2>&1 | tail -20
EOF

echo ""
log_info "========================================"
log_info "Deployment completed!"
log_info "========================================"
