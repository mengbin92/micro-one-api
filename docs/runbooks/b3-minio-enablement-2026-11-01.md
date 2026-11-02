# B3 生产控制面修复与 MinIO 接入

> 2026-11-01 · Asia/Shanghai · [B3 验收与恢复](b3-managed-backup-acceptance-2026-10-30.md) · [MinIO 配置与部署](../../deployments/backup/README.md)

## 结果与边界

2026-11-01 19:38 CST 核验：管理员会话读取 `/api/admin/backups` 与
`/api/admin/backup-policy` 均为 HTTP 200，worker 为 `ready`。MinIO 使用独立
Compose 项目 `micro-one-api-backup`，只绑定 `127.0.0.1:9000`，Console 关闭，
数据卷为 `micro-one-api-backup_minio-data`。host worker 已安装为 systemd 服务，
启动预检通过，定时策略仍关闭。

本次没有创建生产备份任务，没有执行生产全量停写或恢复演练；上线后任务数为 0。
这些检查证明控制面、存储权限与空闲调度链路可用，不能替代真实备份及恢复验收。
同机 MinIO 与业务共用主机和磁盘，仍需要异机副本覆盖主机/磁盘故障。

## 根因与修复

1. 实际 admin-api 容器未设置 `BACKUP_CONTROL_ENABLED`，线上 Compose 也没有该
   环境变量。未创建 backup repo 时，两个 GET 接口均返回 503：
   `managed backup unavailable` / `AUTHORIZATION_DEPENDENCY_UNAVAILABLE`。
   为线上 Compose 增加开关并设置部署 `.env` 为 `true`，只重建 admin-api，
   使用原运行镜像。
2. 四张 Admin 147 控制表已存在，但当前 Admin 运行账号缺少表权限。开启控制面后，
   SQL 日志明确报告 `SELECT command denied`。按
   [147 运行权限模板](../../migrations/manual/147_backup_runtime_grants.example.sql)
   为当前账号补齐四张表权限，两个接口恢复 200。
3. 尚无对象存储和 host worker。使用本机交叉构建的 Linux/amd64 MinIO，创建私有桶
   `private-backups`、限定到 `micro-one-api/production/*` 的独立 worker 账号，
   与 MinIO 管理员凭据分离。S3 凭据与 Docker 权限均不进入 admin-api。
4. `channel-account-ops-worker` 实际运行且写 SQL，却未列入备份 Writers；其 profile
   也会被默认 `compose config` 隐藏。worker 现在展开全部 profile 的配置模型，将该
   服务加入停写、镜像清单和中断恢复集合；实际运行服务缺失于模型时，在任何停写前拒绝。
   读取完整模型不会启动原本停止的 profile 服务。
5. worker 的策略表权限模板只有 SELECT，调度领取任务使用的 `SELECT ... FOR UPDATE`
   被生产 MySQL 拒绝。补充该表 UPDATE 权限并同步修正模板。MySQL 对
   [FOR UPDATE 锁定读](https://dev.mysql.com/doc/refman/8.4/en/innodb-locking-reads.html)
   要求 SELECT 加 UPDATE、DELETE 或 LOCK TABLES 中至少一项。

## 交付与配置位置

- MinIO 固定官方安全发布 `RELEASE.2025-10-15T17-29-55Z`，源码 commit
  `9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a`；mc commit
  `7394ce0dd2a80935aded936b09fa12cbb3cb8096`。使用
  [官方安全修复后的源码](https://github.com/minio/minio/releases/tag/RELEASE.2025-10-15T17-29-55Z)
  构建，未使用旧二进制镜像。上游社区仓库已归档，后续来源需持续审阅。
- MinIO 镜像 ID：`sha256:ec287056b0cc5ecd1a523b1a34f5d5c485833e0d76ad7e27699fc7cb017a2395`。
  交付 TAR SHA256：`7227b3867ca2e61dab3496defc0c5e9c17c08640ba7a1ba86a63ab8ff332d352`。
- 新 worker 为静态 Linux/x86_64，SHA256：
  `3c1171a24849f6a02f0e92c167716ad294ca5aa70cb437bc097dfb1159713cba`。
  两项产物均在开发机构建后传入主机，未在生产编译。
- 存储配置：`/opt/micro-one-api/backup-storage/`；专用 env 为
  `.env.minio.local`，目录 0700、凭据文件 0600。
- worker：`/opt/micro-one-api/bin/backup-worker`；配置/env 位于
  `/etc/micro-one-api/backup-worker.{json,env}`（0600），状态目录为
  `/var/lib/micro-one-api-backup`（0700）。服务为 `backup-worker.service`。
- metadata/snapshot 数据库账号分离，并限制来源为本机 Docker bridge gateway；
  DSN 使用现有 MySQL 容器私网 IP，没有新增数据库端口。MySQL 容器重建后需核对 IP。
- worker 配置覆盖实际 9 个应用 schema 和 19 个恢复配置文件，继续引用在线主
  Compose，不保存可能过期的已展开配置。每次快照会重新获取在线配置和镜像清单。
- age 恢复身份只生成在维护者开发机的
  `~/.local/share/micro-one-api/backup-recovery/production-20261101.agekey`（0600）。
  私钥未上传生产；仅公钥进入 worker 配置。维护者应独立离线保管该文件。

## 验证记录

原始线上反馈环：正常管理员登录取得会话，在主机 loopback 请求两个 GET 接口，
修复前均为上述 503；相同请求修复后均为 200。最终列表响应包含
`worker_phase=ready`、有效 `worker_last_seen` 和 `worker_timeout_minutes=20`。

- 聚焦 HTTP 回归：
  `go test ./app/admin/internal/server -run '^TestManagedBackupHTTPErrorCodesAndClosedRoutes$' -count=1`。
  两条禁用路由都断言用户报告的完整错误 JSON。
- 实际控制面与鉴权：
  `go test ./internal/integration -run '^TestManagedBackupAdminHTTPAndRPC$/sqlite$' -count=1`。
- worker 拓扑与恢复回归：`go test -race ./domain/backup/... -count=1`。
  先复现隐藏/遗漏 writer，再验证停写前拒绝与 account-ops 的恢复。
- MinIO 本机独立项目验证：初始化连续两次通过；真实 `ObjectStore` 的
  Preflight/GetBucketPolicy、条件写入、SHA metadata、内容读回、匿名拒绝、Get、
  PublishReceipt/Receipt、Catalog、Delete 全部通过。测试项目与卷已清理。
- 生产 MinIO 初始化：worker 账号临时对象写入/读取一致，匿名访问拒绝，探针对象删除。
- 生产 worker：`-mode catalog` 成功；确认策略关闭且无 queued/running/deleting 任务后，
  `-mode once` 成功；systemd 为 active/running、NRestarts=0。
- 最终 Admin/Relay `/healthz` 为 200，MySQL/Redis/account-ops/MinIO 健康，业务容器
  无新增重启，无 `resume.json`，备份任务数仍为 0。

## 回滚与后续验收

控制开关变更前的主机备份为 `docker-compose.yml.backup-control-20261101-192309`
和 `.env.backup-control-20261101-192309`；原账号授权记录保存为
`backup-control-grants-before-20261101-192356.txt`，均为私有文件。
需要关闭控制面时将开关设回 false 并只重建 admin-api。worker 可用
`systemctl disable --now backup-worker` 停用；MinIO 可停止容器，但保留数据卷，
禁止 `down -v`。不要撤销已有任务仍需要的数据库权限或删除离线恢复身份。

下一步按 B3 运维窗口执行首个生产手动备份，在独立空环境完成恢复核对并记录实际
RPO/RTO，再决定开启定时策略。不要将 `ready` 当作已有成功备份或恢复 SLA 的证明。
