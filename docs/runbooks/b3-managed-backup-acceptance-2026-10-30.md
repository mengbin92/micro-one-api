# B3 管理化备份实施与恢复验收

> 2026-10-30 · Asia/Shanghai · [冻结契约](../design/b3-managed-backup-contract-2026-10-30.md) · [排期](../design/next-stage-plan-2026-10-26.md)

## 状态与目标

**工程验收通过，生产已部署（默认关闭）；存储与启用验收待外部验证。** 2026-10-30 23:00 CST 起 Identity/Admin/Web 与 Admin 147 已上线，控制面关闭、未启用调度、未安装 host worker、未覆盖任何生产存储（见[生产部署证据](evidence/b3-production-deployment-2026-10-30.json)）。工程默认 RPO 24 小时、RTO 60 分钟；真实私有存储、生产恢复目标及生产数据规模下的恢复时间待维护者补充确认。隔离演练不能替代生产恢复 SLA。生产覆盖恢复不在 B3 MVP 内。

实现包括 Admin 控制页/HTTP/RPC、147 三库控制表、现有全局 IAM 配置权限及 A2 step-up、主机双锁调度、任务状态/worker 在线状态、共同停写一致性、age 公钥加密、S3 校验/外部恢复索引、保留策略和审计。SQLite/PostgreSQL 的后台备份仍未实现；本次三库验证针对控制表、鉴权和迁移。

## 本地验证入口

```bash
make all
make verify
./scripts/test-managed-backup-control.sh
make test-managed-backup
cd web && npm run test:e2e:b3
```

- 控制链路：真实 Admin HTTP 和生成 gRPC → Identity owner → Admin SQL repo，覆盖 legacy/IAM、伪造数字角色、缺少 operator、策略 CAS、请求幂等、actor、无秘密/下载字段。脚本自行创建本机 MySQL 8.4 / PostgreSQL 18，另跑 SQLite，完成后销毁自己的项目和卷，不读取部署 `.env`。
- A2 边界：三库分别验证 `admin.backups` 写证明、跨会话拒绝、五分钟到期和 IAM/legacy 一致性，读状态无需写证明；既有生产强制开关保持原状。
- 存储链路：隔离 MySQL 8.4、Redis 8.4、私有 S3 兼容服务，迁移全量/重复应用；真实共同停写、导出、加密、上传、匿名拒绝及读回；第二个空项目恢复后核对原 API Token、MFA 解密和登录、渠道密钥解密、钱包、未结算预扣重复认领及重复释放只返还一次。
- 故障边界：SQL leader 互斥、归档损坏/截断/越界、错误 receipt、非空恢复拒绝、共同停写后锁失败恢复服务、模拟中断重启日志、S3 不可用仍先恢复服务、保留清理失败可重试、存储命名空间隔离。
- 页面：桌面及移动 Chrome 的策略修改、中文原因、提交结果不确定时复用 request ID、状态/审计及无横向溢出；浏览器使用 UI fixtures，真实 owner 链路另由 Go 验证。

S3 隔离夹具固定为 RustFS 镜像 digest `sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2`，只用于本机测试，不是生产存储选型。`BACKUP_TEST_S3_IMAGE` 可显式覆盖为维护者审阅的兼容镜像。测试记录仅保存镜像、摘要、耗时和检查项目，不保存密钥、Token、数据内容或 DSN。

完整 `make verify`（含 unit/race、架构、迁移/IAM、类型生成与前端门禁）通过；类型再生成使用临时 Git index 中的预期新增类型作为基线，实际用户 index 未改动。最终夹具加密大小 21,010,991 字节，备份 2.151 秒，恢复导入 2.371 秒，下载至 owner 核对完成 3.027 秒。Linux/amd64 静态 worker 及 Linux 下 CLI 启动通过。

最终通过结果及耗时见[工程证据](evidence/b3-managed-backup-2026-10-30.json)。恢复计时分别记录导入阶段和下载至 owner 核对完成；小型夹具的秒级耗时不用于推算生产容量。

## 生产部署（2026-10-30）

- 顺序按冻结契约：先 Admin 147，再 Identity，再 Admin/Web；host worker 无生产私有存储，暂不安装。
- 迁移：`147_create_managed_backups` 落到 `oneapi_admin`（四张控制表；默认 policy `Enabled=false`、`backup_worker_state=offline`）。远程 root TCP 锁定（error 3118），按 B1 惯例由现有 mysql 容器本地 socket 以 root 执行 Runner 同款 SQL，登记 `schema_migrations` 后由仓库 Runner（`b3-migrate:amd64`，`Dockerfile.migrate`）`-ownership admin -status` 复核为 yes。Admin 服务账号无 `schema_migrations` CREATE 权限，不能自行跑 writer。
- 服务：`scripts/deploy-update.sh identity-service admin-api`，部署标签 `deploy-20261030-225430-7d3f4cbf`，源码摘要 `820a1abc…c73`（提交前工作树）。回滚锚点：镜像 tag `rollback-20261030-225430`、`docker-compose.yml.rollback-20261030-225430`、web 备份 `/opt/web/dist.bak.20261030-225808`。
- 核验：两服务 0 重启、healthz ok、启动日志无 panic/fatal/error；`BACKUP_CONTROL_ENABLED` 未设置（容器内 0 处）；未授权探测 `/api/admin/backups` 返回 401；线上 `index.html` 哈希与本地 dist 一致；其余 21 个容器未变。
- Web：hash 资源先铺、最后原子替换 `index.html`，已打开页面保持可用。

## 候选配置和权限

1. 复制并填写 [`worker.example.json`](../../deployments/backup/worker.example.json) 与 [`worker.env.example`](../../deployments/backup/worker.env.example)，文件均 root-owned、0600，state_dir 为 0700。核对实际 Compose 项目名、全部 MySQL schema 和所有配置/证书路径。不要直接运行含 REPLACE 的模板。
2. 在隔离/离线安全位置生成 age X25519 identity，私钥独立托管；worker 配置只放对应 `age1...` public recipient。恢复时才挂载离线 identity。备份也覆盖部署 `.env`/私钥文件，所有此类内容始终在 age 加密对象内。
3. 私有 S3 bucket 由维护者准备；启用 Block Public Access，按 prefix 赋予 ListBucket、GetBucketPolicy、GetObject、PutObject、DeleteObject 及所需 multipart 权限。worker 不创建 bucket、不修改公开策略。版本化、Object Lock、未完成 multipart 和历史版本生命周期按真实存储单独验收；Object Lock 阻止删除时清理保持 deleting/需处理。
4. 主机 worker 必须能通过私网访问同一 MySQL；默认 Compose 不发布 MySQL 端口，模板中的 `127.0.0.1:3306` 仅为示例，可填写实际 Docker bridge 地址或另行审阅的 loopback-only 映射，不开放公网数据库端口。实例 UUID 会与容器内 MySQL 核对，容器重建后应复核连接地址。对 Admin schema 应用 147，按[运行账号权限模板](../../migrations/manual/147_backup_runtime_grants.example.sql)授权，核对原 IAM `resource_write_audits` 权限仍有效。host worker metadata 账号只操作四张控制表，snapshot 账号需要完整 schema 可见性与全局读锁权限；现有 mysql Compose root 凭据仅用于容器内 dump。
5. 按 Identity → Admin/Web → host worker 的顺序更新候选，保持现有 A2 策略。`BACKUP_CONTROL_ENABLED=true` 只开放管理控制，默认数据库 policy 仍为关闭。先手动备份和恢复，维护者验收后再启用 policy。Frontend 仍单独发布宿主机 `web/dist`。

本机/CI 跨构建主机二进制，禁止远程构建：

```bash
docker buildx build --platform linux/amd64 --progress=plain \
  -f deployments/docker/Dockerfile.backup-worker \
  --output type=local,dest=/tmp/backup-worker-linux-amd64 .
```

生成静态 Linux/amd64 可执行文件。将其交付主机的 `/opt/micro-one-api/bin/backup-worker`，安装[systemd 模板](../../deploy/backup-worker.service)，填写实际路径后启动。Docker/Compose CLI 是主机前置条件。worker 是独立的主机权限角色；不得把其权限注入 admin-api。所有实例使用同一私有 state_dir，不能以不同目录绕过主机编排锁。

## 手动备份与调度检查

在 `/admin/options` 填写原因并点击“立即备份”；成功返回 queued 后，等待任务成为 succeeded。备份会有短暂业务停写，传输与校验在业务恢复后进行。核对：

- worker 最近检查时间有效、任务为 succeeded，镜像/版本/配置/密钥引用清单完整；S3 加密对象及外部 receipt 均可读，匿名不可读。
- 原运行服务健康、原停止服务未被启动，无 `resume.json`；MySQL 读锁已释放，Redis/业务可继续工作。
- 下载并恢复到独立空环境完成业务核对，再开启定时策略；检查至少两次实际计划运行与备份年龄。启用之后按 RPO 监控成功备份年龄，不能以 worker 在线替代备份成功。
- 调度失败、uncertain 和 deleting 都保留证据；不要复用修改过 actor/原因的 request ID，不自动重放旧运行任务。

## 主机故障处理

worker 被 SIGKILL/断电后，systemd 重启会先读取私有重启日志并恢复原服务，然后把旧 running 记为 uncertain；即使 S3 不可用也先恢复服务。也可在同一主机、同一配置/state_dir 手动执行：

```bash
/opt/micro-one-api/bin/backup-worker -config /etc/micro-one-api/backup-worker.json -mode recover
```

重启日志损坏、容器缺失或恢复健康失败时保持需处理状态并保留日志；按其中记录的原服务集人工核对，不用 `compose up` 擅自创建/重建其他服务。不能把上传结果不确定的任务直接改为 succeeded；先核对加密对象、完整摘要和外部 receipt，必要时创建独立新任务。切换存储地址时旧命名空间保留，不通过新 bucket 的凭据删除历史对象。

## 隔离空环境恢复

1. 从备份清单定位相同 MySQL/Redis 镜像，准备独立 Compose project 和全新卷。只启动 mysql、redis；新 Redis AOF 关闭。外部网络/供应商流量保持隔离。重建基础 SQL 运行账号/GRANT 不使用生产覆盖操作。
2. 准备目标配置：project、DSN 和 state_dir 指向新环境；schema 集合与原备份一致。不要把解密得到的生产地址直接应用到恢复环境。
3. 即使控制数据库已丢失，也可从私有 S3 外部索引取回备份：

```bash
backup-worker -config /etc/micro-one-api/recovery.json -mode catalog
backup-worker -config /etc/micro-one-api/recovery.json -mode fetch \
  -job-id <verified-job-id> -artifact /private/recovery.tar.gz.age
backup-worker -config /etc/micro-one-api/recovery.json -mode restore -empty-target \
  -artifact /private/recovery.tar.gz.age -sha256 <verified-encrypted-sha256> \
  -identity-file /offline/age-identity.txt
```

`fetch` 验证摘要并输出摘要，`restore` 再次验证；归档内容完整验证后才触碰空目标。任何非系统 MySQL 表、Redis 任意 DB 中的数据、不同存储镜像或已运行的其他容器都会阻止恢复。恢复中途失败时丢弃本次隔离环境，重新创建空目标，不继续覆盖半恢复数据库。

4. 解密配置位于 `<state_dir>/recovered-<job>/config`，权限 0700/0600。核对并使用隔离地址重建配置；补回实际加密/Token hash/MFA 密钥、证书、相同版本镜像与独立 Web dist。MySQL event scheduler 保持关闭，待核对后另行决定；业务服务由操作员明确启动。
5. 验证钱包余额/预扣/账本和原 Token 主键/额度；实际渠道凭证解密；MFA 登录/恢复码及会话 TTL；待结算 reservation/intent/异步任务及幂等键，不重复结算、退款、资产发放或外部执行。恢复出来的 running 备份任务属于历史中断，应标为 uncertain，不自动重新备份/清理未经核对的历史目标。
6. 记录从下载到业务核对完成的时间、snapshot 年龄、真实数据量及差异。工程 fixture 仅证明流程；真实生产 RPO/RTO 和切流决定单独验收。

## 保留项

- 真实私有 S3 凭据、TLS/权限/容量和 bucket 生命周期未配置或验证。
- 生产恢复目标、实际停写窗口、真实容量下 RTO、连续调度观察尚待维护者确认。
- Identity/Admin/Web 与 Admin 147 已于 2026-10-30 部署（控制面关闭）；host worker、运行账号授权与启用待真实私有 S3 后按维护者决定执行；既有计价/认证/支付/供应商试点不变。
- PostgreSQL/SQLite 后台备份、PITR、跨主机自动接管及生产覆盖恢复另排；不创建或恢复任何既有暂停的自动任务。
