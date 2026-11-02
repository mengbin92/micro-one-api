# 本机私有 MinIO 备份存储

`minio.compose.yml` 是独立 Compose 项目，只提供备份对象存储。API 仅发布到
`127.0.0.1:9000`，Console 关闭，数据保存在 `minio-data` 命名卷。主机
`backup-worker` 使用它，admin-api 不接收 S3 凭据或 Docker socket。

`GET /api/admin/backups` 返回 `managed backup unavailable` 还可能是控制面未启用：
admin-api 的实际容器环境必须有 `BACKUP_CONTROL_ENABLED=true`，Admin 147 迁移和
运行账号权限必须完整。仅创建 S3 bucket 不会改变这个开关。

## 镜像先在开发机准备

固定 MinIO `RELEASE.2025-10-15T17-29-55Z`（commit
`9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a`）和 mc
`RELEASE.2025-08-13T08-35-41Z`（commit
`7394ce0dd2a80935aded936b09fa12cbb3cb8096`）。MinIO 的
[该安全发布](https://github.com/minio/minio/releases/tag/RELEASE.2025-10-15T17-29-55Z)
修复了 session policy 权限提升，并要求自行从源码构建容器；不使用缺少该修复的旧镜像。
上游社区仓库已归档，后续更新需重新审阅来源。构建文件固定两个源码 commit，使用
原生构建平台的 Go 交叉编译 Linux 目标，产物同时包含 `minio`、`mc` 和运行检查工具。

在仓库根目录的开发机执行：

```bash
docker buildx build --platform linux/amd64 --load --progress=plain \
  -f deployments/docker/Dockerfile.backup-minio \
  -t micro-one-api/backup-minio:RELEASE.2025-10-15T17-29-55Z .
docker save micro-one-api/backup-minio:RELEASE.2025-10-15T17-29-55Z \
  -o /tmp/backup-minio-image.tar
scp /tmp/backup-minio-image.tar "$DEPLOY_REMOTE_SERVER:/tmp/"
ssh "$DEPLOY_REMOTE_SERVER" 'docker load -i /tmp/backup-minio-image.tar && rm -f /tmp/backup-minio-image.tar'
rm -f /tmp/backup-minio-image.tar
```

禁止在生产服务器构建。Compose 采用 `pull_policy: never`，只运行已交付的镜像。
部署时记录镜像 ID；需要改变来源时显式设置 `BACKUP_MINIO_IMAGE`。

## 初始化私有桶和 worker 账号

将本目录的 `minio.compose.yml`、`minio-init.sh` 和 `.env.minio.example` 放在主机的
专用目录，例如 `/opt/micro-one-api/backup-storage`；不要与业务 Compose 合并。
复制 `.env.minio.example` 为 `.env.minio.local` 并设为 root-owned、0600，替换所有
`REPLACE` 值。管理员和 worker 必须使用不同账号及独立随机密码，可以用
`openssl rand -hex 24` 生成密码。不要将真实文件提交到仓库，也不要把展开环境变量的
`docker compose config` 输出保存到日志；验证仅用 `config --quiet`。

在该主机目录执行：

```bash
docker compose --env-file .env.minio.local -f minio.compose.yml config --quiet
docker compose --env-file .env.minio.local -f minio.compose.yml up -d --wait minio
docker compose --env-file .env.minio.local -f minio.compose.yml --profile setup run --rm --interactive=false -T minio-init
curl --fail http://127.0.0.1:9000/minio/health/ready
```

初始化创建专用 bucket、关闭匿名访问，并配置仅允许目标 bucket 和 prefix 的 worker
账号。它通过临时对象验证 worker 上传/读取及匿名拒绝，随后删除该对象。凭据通过环境、
临时配置和标准输入传递，mc 命令行不包含密码。重复初始化会按 env 中的值更新该 worker
账号及名为 `managed-backup-worker` 的策略；本项目应使用独立 MinIO 实例和专用桶。

不要使用 `docker compose down -v`：它会删除备份数据卷。主机磁盘容量与业务共享，
需为归档及临时空间留出余量。同机 MinIO 可以保存服务备份，但主机或磁盘丢失时仍需
异机副本才能恢复。

## 接入 host worker 和控制面

按 [B3 安装与验收](../../docs/runbooks/b3-managed-backup-acceptance-2026-10-30.md)
准备 host worker、数据库专用权限和离线保管的 age 解密私钥。worker 配置只放对应的
age 公钥；S3 配置可使用下面的本机地址（端口、桶和 prefix 与 env 保持一致）：

```json
{
  "endpoint": "http://127.0.0.1:9000",
  "bucket": "private-backups",
  "prefix": "micro-one-api/production",
  "region": "us-east-1",
  "access_key_env": "BACKUP_S3_ACCESS_KEY",
  "secret_key_env": "BACKUP_S3_SECRET_KEY"
}
```

将 worker 的两项 S3 凭据放入 root-owned、0600 的
`/etc/micro-one-api/backup-worker.env`。MinIO 管理员凭据只保留在存储初始化配置中。
worker 的 `files` 清单应包含业务部署配置、MinIO Compose 及其专用 env，便于恢复
部署；这些文件只进入 age 加密归档，恢复身份独立保管。

为 admin-api 的实际 Compose `environment` 增加
`BACKUP_CONTROL_ENABLED=${BACKUP_CONTROL_ENABLED:-false}`，设置部署 `.env` 中的
`BACKUP_CONTROL_ENABLED=true`，再仅重建 admin-api 容器。先保持数据库 backup policy
的 `enabled=false`，检查管理员会话读取 `/api/admin/backups` 和 `/api/admin/backup-policy`
均为 200。启动 worker 后应显示最近心跳；经手动备份、隔离空环境恢复验收后再启用
定时策略。手动备份会短暂停写，必须按运维窗口执行。
