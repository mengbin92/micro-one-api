#!/bin/bash
# scripts/deploy.sh
# 跨平台 Docker 镜像构建与部署脚本
#
# 必填环境变量（从 .env 或 shell 环境读取；不要把真实值写进 git 跟踪的文件）：
#   DEPLOY_REMOTE_SERVER     例：root@10.0.0.1
#   DEPLOY_REMOTE_DIR        例：/opt/micro-one-api
#
# 可选：
#   DEPLOY_TARGET_PLATFORM   默认 linux/amd64
#   DEPLOY_OUTPUT_DIR        默认 ./build/docker-images
#   DEPLOY_SERVICES          空格分隔的服务名列表；不设则用内置默认顺序
#   DEPLOY_DRY_RUN           设为 1 时只打印命令不执行
#   DEPLOY_SKIP_MIGRATIONS   设为 1 时跳过数据库迁移步骤
#   DEPLOY_SKIP_UPLOAD       设为 1 时跳过镜像上传步骤
#   DEPLOY_SKIP_RESTART      设为 1 时跳过服务重启步骤
#   DEPLOY_SKIP_FRONTEND     设为 1 时跳过前端构建

set -euo pipefail

# ---- 颜色 ----
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# ---- 加载 .env（不覆盖已有 shell 环境变量）----
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
if [ -f "${REPO_ROOT}/.env" ]; then
    # .env 里其它变量（如 DATABASE_DSN）含特殊字符，不能直接 . 整个文件。
    # 这里只挑 DEPLOY_* 前缀的变量导入；shell 环境中已有的同名变量优先。
    while IFS= read -r line; do
        case "${line}" in
            ''|'#'*) continue ;;
        esac
        if [[ "${line}" =~ ^(DEPLOY_[A-Z0-9_]+)=(.*)$ ]]; then
            key="${BASH_REMATCH[1]}"
            val="${BASH_REMATCH[2]}"
            # 去掉行尾注释（不在引号内的 #）
            case "${val}" in
                *\\#*) : ;;
                *) val="${val%%#*}" ;;
            esac
            # 去首尾空白与外层单/双引号
            val="${val#"${val%%[![:space:]]*}"}"
            val="${val%"${val##*[![:space:]]}"}"
            if [[ "${val}" =~ ^"(.*)"$ ]] || [[ "${val}" =~ ^\'(.*)\'$ ]]; then
                val="${BASH_REMATCH[1]}"
            fi
            if [ -z "${!key+x}" ]; then
                export "${key}=${val}"
            fi
        fi
    done < "${REPO_ROOT}/.env"
fi

# ---- 必填配置校验 ----
: "${DEPLOY_REMOTE_SERVER:?DEPLOY_REMOTE_SERVER 未设置（请在 .env 中配置，形如 root@10.0.0.1）}"
: "${DEPLOY_REMOTE_DIR:?DEPLOY_REMOTE_DIR 未设置（请在 .env 中配置，形如 /opt/micro-one-api）}"

# ---- 可选配置 + 默认值 ----
TARGET_PLATFORM="${DEPLOY_TARGET_PLATFORM:-linux/amd64}"
OUTPUT_DIR="${DEPLOY_OUTPUT_DIR:-${REPO_ROOT}/build/docker-images}"
DRY_RUN="${DEPLOY_DRY_RUN:-0}"
SKIP_MIGRATIONS="${DEPLOY_SKIP_MIGRATIONS:-0}"
SKIP_UPLOAD="${DEPLOY_SKIP_UPLOAD:-0}"
SKIP_RESTART="${DEPLOY_SKIP_RESTART:-0}"
SKIP_FRONTEND="${DEPLOY_SKIP_FRONTEND:-0}"

DEFAULT_SERVICES=(
    "identity-service"
    "channel-service"
    "billing-service"
    "config-service"
    "log-service"
    "monitor-worker"
    "notify-worker"
    "relay-gateway"
    "admin-api"
)

if [ -n "${DEPLOY_SERVICES:-}" ]; then
    # shellcheck disable=SC2206
    SERVICES=( ${DEPLOY_SERVICES} )
else
    SERVICES=( "${DEFAULT_SERVICES[@]}" )
fi

# ---- 工具函数 ----
log()  { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*"; }
warn() { echo -e "${YELLOW}[$(date +%H:%M:%S)] WARN${NC} $*"; }
err()  { echo -e "${RED}[$(date +%H:%M:%S)] ERROR${NC} $*" >&2; }

# run_cmd: 在 DRY_RUN=1 时只打印
run_cmd() {
    if [ "${DRY_RUN}" = "1" ]; then
        echo -e "${YELLOW}[DRY-RUN]${NC} $*"
    else
        "$@"
    fi
}

# ---- 预检 ----
if [ "${DRY_RUN}" != "1" ]; then
    if ! command -v docker >/dev/null 2>&1; then
        err "docker 未安装"; exit 1
    fi
    if ! docker info >/dev/null 2>&1; then
        err "Docker daemon 未运行，请先启动 Docker"; exit 1
    fi
fi

# SSH 连接检查（也用 -o BatchMode=yes 避免误吞密码提示）
log "检查 SSH 连接 ${DEPLOY_REMOTE_SERVER} ..."
if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "${DEPLOY_REMOTE_SERVER}" "echo 'SSH连接成功'" >/dev/null 2>&1; then
    err "无法连接到远程服务器 ${DEPLOY_REMOTE_SERVER}（请检查 ~/.ssh/config 或密钥是否配置）"
    exit 1
fi

mkdir -p "${OUTPUT_DIR}"

# ---- 步骤 1: 构建前端 ----
if [ "${SKIP_FRONTEND}" != "1" ]; then
    log "=========================================="
    log "步骤 1/5: 构建前端资源"
    log "=========================================="
    if [ ! -d "${REPO_ROOT}/web" ]; then
        err "${REPO_ROOT}/web 目录不存在，跳过前端构建"
    else
        run_cmd bash -c "cd '${REPO_ROOT}/web' && npm ci && npm run build"
        log "前端构建完成"
    fi
else
    warn "跳过前端构建（DEPLOY_SKIP_FRONTEND=1）"
fi
echo

# ---- 步骤 2: 构建镜像 ----
log "=========================================="
log "步骤 2/5: 构建 ${TARGET_PLATFORM} Docker 镜像"
log "=========================================="

BUILDX_CACHE_FLAGS=()
# 如果 registry 缓存可访问（可选），启用 buildx cache-to/from；这里默认只用本地 cache。
for SERVICE in "${SERVICES[@]}"; do
    log "构建 ${SERVICE} ..."
    run_cmd docker buildx build \
        --platform "${TARGET_PLATFORM}" \
        --build-arg "SERVICE_NAME=${SERVICE}" \
        --file "${REPO_ROOT}/deployments/docker/Dockerfile" \
        --tag "micro-one-api/${SERVICE}:latest" \
        --load \
        "${REPO_ROOT}"

    run_cmd docker save "micro-one-api/${SERVICE}:latest" \
        -o "${OUTPUT_DIR}/${SERVICE}.tar"
    log "✓ ${SERVICE} 构建完成"
done
log "所有镜像构建完成"
echo

# ---- 步骤 3: 上传镜像（流式 gzip） ----
if [ "${SKIP_UPLOAD}" != "1" ]; then
    log "=========================================="
    log "步骤 3/5: 上传镜像到 ${DEPLOY_REMOTE_SERVER}"
    log "=========================================="
    for SERVICE in "${SERVICES[@]}"; do
        log "上传 ${SERVICE}.tar (gzip) ..."
        run_cmd bash -c "gzip -c '${OUTPUT_DIR}/${SERVICE}.tar' | ssh '${DEPLOY_REMOTE_SERVER}' 'gunzip | docker load'"
        log "✓ ${SERVICE} 上传并加载完成"
    done
    log "所有镜像上传完成"
else
    warn "跳过镜像上传（DEPLOY_SKIP_UPLOAD=1）"
fi
echo

# ---- 步骤 4: 数据库迁移 ----
if [ "${SKIP_MIGRATIONS}" != "1" ]; then
    log "=========================================="
    log "步骤 4/5: 执行数据库迁移"
    log "=========================================="
    REMOTE_MIG_DIR="${DEPLOY_REMOTE_DIR}/migrations-new"
    run_cmd ssh "${DEPLOY_REMOTE_SERVER}" "rm -rf '${REMOTE_MIG_DIR}' && mkdir -p '${REMOTE_MIG_DIR}'"
    run_cmd scp -r "${REPO_ROOT}/migrations/." "${DEPLOY_REMOTE_SERVER}:${REMOTE_MIG_DIR}/"

    # 用单引号 heredoc 防止 shell 注入；用 BatchMode 和 fail-fast 让 ssh 行为可控
    if [ "${DRY_RUN}" != "1" ]; then
        ssh -o BatchMode=yes "${DEPLOY_REMOTE_SERVER}" bash -s <<'REMOTE_EOF'
set -euo pipefail

MYSQL_CONTAINER=$(docker ps --filter "name=mysql" --format "{{.Names}}" | head -n1)
if [ -z "${MYSQL_CONTAINER}" ]; then
    echo "ERROR: MySQL 容器未运行" >&2
    exit 1
fi

# 通过 docker exec 读取密码；用临时文件传密码避免 -p 出现在 ps
MYSQL_PASSWORD_FILE=$(mktemp)
docker exec "${MYSQL_CONTAINER}" printenv MYSQL_ROOT_PASSWORD > "${MYSQL_PASSWORD_FILE}"

# mysql 客户端便捷封装
mysql_exec() {
    docker exec -i "${MYSQL_CONTAINER}" mysql --defaults-extra-file=<(printf '[client]\npassword="%s"\n' "$(cat "${MYSQL_PASSWORD_FILE}")") oneapi "$@"
}

# 确保 schema_migrations 表存在
mysql_exec <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
    version VARCHAR(255) PRIMARY KEY,
    applied_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
SQL

MIG_DIR="/opt/micro-one-api/migrations-new"
shopt -s nullglob
# 跳过以 ~ 或 .bak 结尾的临时文件；按字典序排序
MIGRATIONS=( "${MIG_DIR}"/*.sql )
if [ "${#MIGRATIONS[@]}" -eq 0 ]; then
    echo "no migration files in ${MIG_DIR}"
else
    for migration in "${MIGRATIONS[@]}"; do
        filename=$(basename "${migration}")
        version="${filename%.sql}"
        # 已执行过则跳过
        applied=$(mysql_exec -N -B -e "SELECT 1 FROM schema_migrations WHERE version='${version}' LIMIT 1;" 2>/dev/null || true)
        if [ "${applied}" = "1" ]; then
            echo "skip ${filename} (already applied)"
            continue
        fi
        echo "apply ${filename} ..."
        mysql_exec < "${migration}"
        mysql_exec -e "INSERT INTO schema_migrations (version, applied_at) VALUES ('${version}', NOW());"
    done
fi
rm -f "${MYSQL_PASSWORD_FILE}"
REMOTE_EOF
    fi
    log "数据库迁移完成"
else
    warn "跳过数据库迁移（DEPLOY_SKIP_MIGRATIONS=1）"
fi
echo

# ---- 步骤 5: 更新服务 ----
if [ "${SKIP_RESTART}" != "1" ]; then
    log "=========================================="
    log "步骤 5/5: 更新服务"
    log "=========================================="
    if [ -f "${REPO_ROOT}/deployments/docker-compose/docker-compose.yml" ]; then
        run_cmd scp "${REPO_ROOT}/deployments/docker-compose/docker-compose.yml" \
            "${DEPLOY_REMOTE_SERVER}:${DEPLOY_REMOTE_DIR}/"
    else
        warn "未找到 deployments/docker-compose/docker-compose.yml，跳过上传"
    fi

    if [ "${DRY_RUN}" != "1" ]; then
        ssh -o BatchMode=yes "${DEPLOY_REMOTE_SERVER}" bash -s <<'REMOTE_EOF'
set -euo pipefail
cd /opt/micro-one-api

# 已通过 scp 上传新 compose；用 `up -d` 让它重新创建变更过的容器
if docker-compose up -d; then
    :
else
    echo "WARN: docker-compose up -d 失败，尝试 docker compose（v2 插件）" >&2
    docker compose up -d
fi

# 清理悬空镜像
docker image prune -f
REMOTE_EOF
    fi
    log "服务更新完成"
else
    warn "跳过服务重启（DEPLOY_SKIP_RESTART=1）"
fi
echo

log "=========================================="
log "  部署完成！"
log "=========================================="
echo
echo -e "查看服务状态: ssh ${DEPLOY_REMOTE_SERVER} 'cd ${DEPLOY_REMOTE_DIR} && docker-compose ps'"
echo -e "查看日志:     ssh ${DEPLOY_REMOTE_SERVER} 'cd ${DEPLOY_REMOTE_DIR} && docker-compose logs -f'"
