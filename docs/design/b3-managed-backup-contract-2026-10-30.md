# B3 管理化备份 MVP 冻结契约

> 2026-10-30 · Asia/Shanghai · 对应 [10 月 26 日计划 B3](next-stage-plan-2026-10-26.md)。交付 MySQL + 单实例 Redis 的管理化备份和隔离空环境恢复；生产覆盖恢复另排。

## 1. 范围及恢复目标

工程默认目标为 **RPO ≤24 小时、RTO ≤60 分钟**，默认每 24 小时一个 UTC 对齐调度槽、保留最近 7 个成功备份。它们是恢复演练的目标值，生产目标和真实数据规模下的耗时仍需维护者确认。策略可配置间隔 1–168 小时、保留 2–90 份、RPO 不小于间隔、RTO 1–1440 分钟。启用调度前必须完成一次手动备份、隔离恢复及目标复核；成功备份年龄超出 RPO 时，不能继续声称满足目标。

覆盖同一 MySQL 实例中全部非系统 schema、Redis 全部逻辑 DB、实际运行容器的镜像 ID、源码/部署版本引用、解析后的 Compose 配置、显式配置文件及密钥引用清单。私有文件随数据库一起加密；密钥引用本身只记录变量名。恢复核对包含钱包、原 API Token、渠道凭证解密、MFA 解密/登录，以及未结算预扣的幂等继续处理。

不覆盖 PostgreSQL/SQLite 的后台导出、Redis Cluster/副本、在线无停机备份、binlog/PITR、生产覆盖恢复、公开下载、备份文件浏览、远程任意命令或自动资金冲正。MySQL 系统用户/GRANT 不在业务 dump 中，隔离恢复时按部署契约重新配置运行账号；不恢复供应商资金或重放真实付费请求。

## 2. 所有权与入口

- Admin 负责控制元数据，`domain/backup/biz` 定义 DO/用例/端口，`domain/backup/data` 实现存储和备份客户端。service 只做 proto ↔ DO 转换。
- 主机上的 `backup-worker` 负责固定 Compose 编排、SQL 导出、Redis RDB、age 加密、私有 S3、恢复和清理。Admin 不接收 Docker socket、快照账号、S3 密钥或 age 私钥。
- 管理页面位于 `/admin/options` 的“管理化备份”。共享已有平台配置权限，不新增角色或隐式授权。

| HTTP | RPC（AdminService） | 固定执行点 / 权限 |
| --- | --- | --- |
| GET `/api/admin/backup-policy` | `GetBackupPolicy` | `admin.backups` / `system.option.read` |
| PUT `/api/admin/backup-policy` | `UpdateBackupPolicy` | `admin.backups` / `system.option.security.update` |
| GET `/api/admin/backups` | `ListBackups` | `admin.backups` / `system.option.read` |
| POST `/api/admin/backups` | `CreateBackup` | `admin.backups` / `system.option.security.update` |

以上权限均要求平台全局范围。HTTP 执行现有 console guard，RPC 独立传递 operator session；只有 service token 不足以操作备份。legacy 模式重新验证实际管理员身份；IAM 忽略浏览器提供的数字角色。写操作在 Identity `admin.backups` 执行点调用 A2 的 `RequireStepUp`，沿用现有强制开关和五分钟会话证明，不改变既有生产开关。HTTP 与直接 RPC 走同一用例。

写操作必须携带 3–240 字符原因；策略使用显式 revision CAS，冲突返回 409，页面保留原草稿。手动任务使用 16–80 位受限 request ID；同 actor、原因和 ID 重试返回原记录，修改原因/actor 产生冲突。页面在提交结果不确定时复用原 ID。不存在下载、删除、覆盖恢复或由请求指定 schema/Compose/存储地址的接口。

## 3. 一致性及可恢复清单

1. 检查私有 S3、完整 schema 集合、配置文件、public age recipient、Compose 与 control/snapshot DSN 指向同一 MySQL UUID；拒绝正在运行的 migrate 和未审阅的额外 Compose 存储写者。Redis 必须为 standalone primary，RDB 路径固定 `/data/dump.rdb`。
2. 将原来运行的九种业务服务与 Redis 写入主机私有、fsync 的重启日志。只停止原来运行的服务；原先停止的服务保持停止。名单固定为 Relay、Admin、Identity、Channel、Billing、Config、Log、Monitor、Notify。
3. 对业务服务发送停止信号并等待最多 60 秒，然后由独立连接执行 `FLUSH TABLES WITH READ LOCK`。同步 Redis `SAVE`，停止 Redis。此时 SQL 停写、Redis 停止，记录 `snapshot_at`。
4. 同一次 `mysqldump --single-transaction --quick --skip-lock-tables --no-tablespaces --set-gtid-purged=OFF --routines --events --triggers --databases ...` 导出全部业务 schema；复制停止态 Redis RDB。锁连接在导出期间保持存活。
5. 无论成功、普通失败还是取消，先解锁并恢复原服务集合，等待其运行/健康状态。加密与上传在业务服务恢复后进行。恢复服务失败时保留重启日志，任务不能记成功。
6. 私有暂存目录中形成包含逐文件 SHA256/大小的 manifest 和 tar.gz，由 age X25519 公钥流式加密。worker 不持有解密私钥。S3 归档和外部 receipt 使用条件写入，拒绝覆盖已有同名对象；写入后检查大小/摘要元数据、实际读回摘要及匿名 HEAD 被拒绝，再发布私有外部 receipt；最后写入 SQL 成功状态。

MySQL 一致读本身不能保证 Redis 同点，因此本项明确使用上述共同停写方案。参考 [MySQL mysqldump 文档](https://dev.mysql.com/doc/refman/8.0/en/mysqldump.html)与 [Redis SAVE 文档](https://redis.io/docs/latest/commands/save/)。Redis 过期时间沿用原绝对时钟，恢复不会延长验证码、会话或限流 TTL。被强制中断的请求可能留下真实未结算任务；恢复时核对并走既有 owner 幂等路径，不凭备份流程补造金额。

配置文件列表必须覆盖实际启用的配置、外部证书/私钥文件与恢复所需配置引用。示例路径需要按真实部署填写；worker 会拒绝缺失、符号链接或非普通文件。`compose.json` 与仅含配置/密钥引用及公钥的 `worker-config.json` 为保留文件名，不能被自定义文件覆盖。宿主机 Web dist 与容器镜像分别登记版本，按相同版本重建/恢复前端。

## 4. 调度、状态、保留与审计

主机文件锁保护编排及故障清理，MySQL `GET_LOCK('micro-one-api:backup:v1',0)` 保护跨连接 leader。锁覆盖调度、导出、上传与保留清理；连接丢失会取消当前工作。相同 Compose 项目的实例必须使用同一 `state_dir`；worker 不支持跨主机接管未共享的本机 Docker/重启日志。

调度每 15 秒检查一次。policy revision + UTC slot 构成不可变调度 ID；错过的历史槽不批量补跑。策略更新会形成新 revision；手动任务可在调度关闭时发起。队列同时最多一个 queued/running 手动任务。运行时间受 worker `timeout_minutes`（1–60）限制。

| 状态 | 含义 |
| --- | --- |
| queued | 持久接收，尚未执行 |
| running | leader 已认领，冻结该任务策略 |
| succeeded | 加密对象、私有性、读回摘要、外部 receipt 与服务恢复均通过 |
| failed | 明确失败，未承诺可恢复对象 |
| uncertain | worker 中断、上传结果不明或服务恢复不完整；不自动重放原任务 |
| deleting | 已进入保留清理，支持中断后幂等继续 |
| expired | 保留清理完成；审计和任务记录保留 |

worker 重启后先恢复原服务、清理受控的中断暂存目录、将旧 running 标为 uncertain，再检查 S3。存储故障不会阻止服务恢复。任务状态之外提供 worker 最后检查时间、阶段和静态错误码，页面能识别离线/需处理状态。

保留策略只清理**当前存储命名空间**超出数量的 succeeded，始终至少保留两份；failed/uncertain 不自动删除。存储命名空间是 endpoint + bucket + prefix 的摘要，凭据轮换不改变它。更换 bucket/prefix 不删除旧目标，旧对象需要独立核对后处理。先记 deleting、删除对象与外部 receipt，再记 expired；清理失败可重试。未完成分片上传由 bucket 生命周期清理，配置版本化 bucket 时同时规划旧版本保留/成本。

policy、手动排队、执行、成功/失败/不确定及保留清理写入 append-only `backup_events`；IAM 管理写审计与业务变更同事务。审计只存 actor、任务 ID、原因、时间和静态错误，不存 DSN、密钥、SQL 或文件内容。

## 5. 存储和恢复

S3 只允许 HTTPS；本机回环隔离测试可使用 HTTP。已有私有 bucket 由维护者准备；worker 不创建 bucket、不修改 ACL/policy，拒绝匿名 Allow policy，并验证实际对象匿名访问被拒绝。建议同时启用 Block Public Access、版本化、按 prefix 限权和适当的未完成 multipart 生命周期。

对象为 `<prefix>/<job>.tar.gz.age`；另有 `<job>.receipt.json` 保存 job、对象 key、加密摘要、大小和 snapshot 时间。`catalog`/`fetch -job-id` 直接使用私有 S3 receipt，控制数据库丢失后仍能恢复。保留副本与 age 私钥的离线托管必须独立于业务主机。加密采用 [age 的标准流式格式](https://pkg.go.dev/filippo.io/age)，不自创加密协议。

恢复命令要求独立 Compose project、空 MySQL（所有非系统表均为空）、空 Redis 所有 DB、Redis AOF 关闭、原 MySQL/Redis 镜像 ID，以及只有两个存储容器运行。先验证加密对象 receipt，完整认证 age EOF、manifest、全部文件哈希及 schema 集合，再导入；空目标的 MySQL event scheduler 在导入前关闭，恢复后保留关闭，避免事件自行执行。任一归档校验失败不能开始写目标。解包拒绝路径穿越、链接、重复条目及超限归档。

恢复保留原业务主键、幂等键、定价快照、钱包、Token 和任务状态。配置解密到私有 review 目录，不自动覆盖目标配置、不启动业务服务、不连接生产供应商。恢复后逐项核对，再由独立启动流程决定何时开放目标流量。非空目标重复恢复明确拒绝。

## 6. 迁移及候选部署顺序

147 为 Admin-owned 的四张控制表：`backup_policies`、`backup_jobs`、`backup_events`、`backup_worker_state`，有 MySQL/PostgreSQL/SQLite 镜像。默认 policy 关闭，worker 离线。三库镜像保证管理接口/迁移兼容，不代表三种存储的备份支持。

候选影响 Identity、Admin、Web 和新增 host worker；不需要修改 Billing/Channel/Relay/Log 二进制。先应用 Admin 147 并授予运行账号必要权限，再更新含 `admin.backups` 执行点及 A2 检查的 Identity，更新 Admin/Web，安装私有 host worker。保持 `BACKUP_CONTROL_ENABLED=false` 和 policy 关闭，完成真实存储及手动恢复演练后再按维护者决定启用。前端沿用宿主机 `web/dist` 发布方式。

回滚先关闭调度和控制入口、停止 worker、确认无重启日志且服务已恢复；控制表保留。Admin 可回滚到无入口版本；备份控制启用期间不能回滚 Identity 到未识别该执行点/检查的版本。当前备份清单引用的应用镜像、Web 版本、证书和离线解密私钥应有对应保留策略。
