# Micro-One-API v0.39.1 发布：备份停写一致性修复与私有 MinIO 部署归档

> 2026-11-02 · 上一版：[v0.39.0](./release-v0.39.0.md)（2026-11-02）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.39.1)

v0.39.1 是 v0.39.0 之后的 **PATCH 修复版本**：修复备份 worker 遗漏已运行的 profile writer、运行拓扑缺失时继续备份及 MySQL 策略锁定读权限不足的问题，并将已上线的私有 MinIO 配置、构建来源和运维证据纳入版本管理。无新增公开 API、数据库迁移或备份归档格式变更。

生产控制面、私有 MinIO 和 host worker 已于 2026-11-01 接通；2026-11-02 只读复核确认控制开关开启、MinIO 健康且仅监听 loopback、worker 为 ready。**定时策略仍关闭，生产备份任务数为 0，全量备份与隔离恢复演练尚未执行。** 本次 tag 发布源码及常规多架构服务镜像，不自动部署生产，也不将 ready 状态视为已有成功备份。部署来源、产物哈希与恢复边界见 [B3 MinIO 接入记录](../runbooks/b3-minio-enablement-2026-11-01.md)。

## 修复内容

### 1. 备份停写覆盖正在运行的 profile writer

**根因**：`channel-account-ops-worker` 实际写入 SQL，却未列入备份 Writers；它所在的 profile 又会被默认 `docker compose config` 隐藏。备份可能遗漏正在运行的写入者，破坏共同停写边界。

**修复**：读取全部 profile 的配置模型，将 account-ops 纳入镜像清单、停写集合及中断恢复集合；发现实际运行服务缺失于模型时，在存储操作、停写和创建恢复日志之前拒绝快照。展开配置仅用于盘点，不会启动原本停止的服务。新增隐藏 writer、未知 writer 和恢复回归测试。

**影响服务**：host `backup-worker`；备份期间的 `channel-account-ops-worker` 停写与恢复。

### 2. 策略锁定读权限与控制面错误回归

**根因**：worker 的权限模板仅授予策略表 SELECT，而调度领取使用 `SELECT ... FOR UPDATE`，MySQL 还要求 UPDATE、DELETE 或 LOCK TABLES 中至少一项。此前管理 GET 接口的 503 则由未开启备份控制面和缺少 Admin 运行权限共同造成。

**修复**：权限模板补充策略表 UPDATE；生产已按现有账号完成授权。为 `/api/admin/backups` 和 `/api/admin/backup-policy` 补充完整 503 错误 JSON 回归，明确依赖不可用时的接口契约。控制开关及 Admin 表权限的现场修复记录同步归档。

**影响服务**：host `backup-worker` 的 metadata 账号、`admin-api` 控制面部署配置与回归测试。无新增表或自动授权迁移。

### 3. 私有 MinIO 部署配置与生产证据入库

**根因**：生产存储和 worker 已安装，但部署配方、初始化流程及验收证据未提交，旧 runbook 仍描述未安装 worker 的状态，无法从 Git 重建已验证的部署。

**修复**：固定 MinIO 与 mc 的源码提交，提供开发机交叉构建 Dockerfile、独立 Compose、loopback 端口、持久化卷、私有桶及限定对象前缀的 worker 账号初始化脚本。只提交凭据占位模板，记录产物哈希、离线恢复身份保管和回滚边界；调整 mc revision 元数据标签，消除公开源码哈希触发的 Gitleaks 误报。

**影响服务**：独立 MinIO 存储项目、host worker 的部署与恢复流程。真实密码、专用 env 和 age 恢复私钥不进入仓库。

## 兼容性说明

- **API 与数据**：既有端点、备份归档格式及业务数据契约保持兼容；无新增迁移，147 运行权限模板需按实际账号、来源 host 和 schema 审阅应用。
- **配置**：使用既有 `BACKUP_CONTROL_ENABLED` 和 worker 配置契约；MinIO 为独立可选存储部署，管理员与 worker 凭据分离。定时策略继续保持现场关闭状态。
- **交付**：Release 工作流发布常规 9 个业务服务的 Linux/amd64、Linux/arm64 镜像；`backup-worker` 与 MinIO 不在该发布矩阵中，按各自专用 Dockerfile 单独构建和交付。
- **恢复边界**：同机 MinIO 不能覆盖主机或磁盘丢失，异机副本、容量规划、首个生产全量备份和恢复 RPO/RTO 验收仍待完成。
- **回滚**：保留 MinIO 数据卷、配置、账号权限和离线解密身份；禁止 `down -v`。仅在 worker 空闲且恢复日志已处理的运维窗口替换程序或停用服务。

## 升级步骤

1. 获取 `v0.39.1`，对照 [MinIO 部署说明](../../deployments/backup/README.md) 和 [B3 验收与恢复](../runbooks/b3-managed-backup-acceptance-2026-10-30.md) 核对现有控制开关、数据库账号和存储配置；已完成现场授权的生产无需重复创建账号或重置初始化策略。
2. 后续更新 host worker 时，从该 tag 在开发机交叉构建 Linux/amd64 静态程序并记录 SHA256；确认无在途任务、无待处理 `resume.json` 后，再于运维窗口替换 `/opt/micro-one-api/bin/backup-worker` 并重启 systemd 服务。生产主机不执行构建。
3. 新建 MinIO 环境时，按专用 Dockerfile 在开发机构建镜像并传入主机；使用私有 env 初始化独立项目与桶，保持 API 仅绑定 loopback，保留持久化卷。
4. 检查两个管理 GET 接口为 200、worker 为 ready、MinIO 健康、worker 具有策略表 SELECT/UPDATE 权限。保持定时策略关闭，按单独运维窗口完成生产手动备份及隔离空环境恢复后，再决定是否启用调度。

## 验证

- `go test -race ./domain/backup/... -count=1` 通过，覆盖拓扑盘点、拒绝遗漏服务与 account-ops 恢复。
- `go test ./app/admin/internal/server -run '^TestManagedBackupHTTPErrorCodesAndClosedRoutes$' -count=1` 通过。
- `sh -n deployments/backup/minio-init.sh`、示例环境的 Compose 配置校验、`git diff --check` 通过。
- Gitleaks 全历史扫描通过，未发现泄漏；提交信息的根因与影响正文检查通过。
- 生产只读复核：MinIO 配置和初始化脚本与入库版本一致，worker 来源记录匹配已部署的 writer 修复；MinIO healthy、worker ready、心跳有效、策略关闭、任务数 0，账号权限与模板一致。
- 生产全量备份、隔离恢复和 RPO/RTO 验收仍待执行；本次发版不将这些保留项标为完成。

## 完整变更日志（v0.39.0 → v0.39.1）

- `fix(backup): include profiled writers and correct worker grants`
- `chore(backup): version production MinIO deployment and runbooks`
- `docs(release): v0.39.1`
