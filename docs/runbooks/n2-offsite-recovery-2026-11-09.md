# N2：异机直连备份与生产归档隔离恢复

> 2026-11-09 · 北京时间 · [当前计划](../design/next-stage-plan-2026-11-08.md#n2复用现有备份和恢复流程) · [B3 恢复手册](b3-managed-backup-acceptance-2026-10-30.md) · [脱敏证据](evidence/n2-offsite-recovery-2026-11-09.json)

## 状态与授权

**首次生产备份、异机独立读回和隔离恢复通过；每天备份已启用，实际日调度观察为 1/2，N2 尚未整体完成。** 维护者指定现有异机、授权在 `/opt` 建独立目录，随后明确确认现在执行停写窗口，并选择直接按每天周期验收；没有临时缩短周期或修改时钟。

沿用 MySQL + 单实例 Redis、现有控制面及 host worker。恢复目标沿用 RPO 24 小时/RTO 60 分钟、保留 7 份。单次生产数据恢复耗时已有实测，持续 RPO 及第二个实际日调度槽仍待观察。本轮另发现并修复 worker 的同槽轮询 409；未发布新版本或覆盖生产数据库。

| 状态 / 优先级 | 项目 | 实际结果 |
| --- | --- | --- |
| ✅ P1 | 异机直连 | 独立私有对象存储位于 `/opt/micro-one-api-backup`；HTTPS 证书校验、限定生产来源的防火墙、独立管理员/worker 凭据、限定对象前缀及匿名拒绝通过 |
| ✅ P1 | 首个生产全量备份 | `n2-production-20261109-first`，12:19:33–12:20:48，75 秒，32,667,974 字节；原容器恢复且 ID/镜像未变 |
| ✅ P1 | 无控制库恢复入口 | 开发机经 SSH 隧道独立访问异机 S3，`catalog`/`fetch` 不连接生产控制库；摘要与 owner 元数据相符 |
| ✅ P1 | 生产归档恢复 | 相同 MySQL/Redis 镜像、独立 Compose 项目及空卷；9 个 schema、24 个归档文件，解密后 299,837,819 字节；88 张有数据表的 INSERT 内容逐表完全相同 |
| ✅ P1 | 业务 owner 核对 | 恢复原 SQL 运行凭据和实际 GRANT；原会话、Token 记录、真实渠道解密、钱包、隔离 MFA 及正金额预扣幂等通过，范围见下节 |
| ✅ P1 | 首个实际日槽 | `schedule-2-20766`，12:39:42–12:40:55，73 秒，32,637,867 字节；停写事件跨度 27.106 秒，原 writer 恢复、健康接口 200 |
| ⚠️ P1 | 连续调度 | 仅 1 个实际日调度槽通过；第二槽预计 11 月 10 日 08:00，不能用手动备份充抵 |

## 存储、容量与恢复材料

- 复用已审阅、固定源码版本的存储镜像，从既有主机导出再交付，没有在服务器构建。两端使用不同 Docker 镜像后端，显示的 image ID 格式不同，实际 RootFS 和运行配置逐项相同。恢复用 MySQL/Redis 则在开发机核对到了归档要求的相同 image ID。
- 存储进程限制 256 MiB 内存，初始化约 81 MiB，上传期间采样约 95 MiB，检查时 healthy 且无 OOM。两份归档及恢复材料落地后磁盘余量约 2.19 GiB；当前约 31 MiB/份，7 份归档约 218 MiB，仍需随数据增长复核。
- 实际桶未启用版本化，成功归档由 worker 的保留数量管理，没有另外配置按天删除成功对象的规则。现场回读的未完成分片原生清理为过期 24h、每 6h 扫描，当前未完成分片列表为空；本轮没有等待过期并实测删除，不将该配置回读记作过期删除实测。
- TLS 证书包含连接地址所需 SAN，由维护者侧 CA 签发；worker 使用专用 CA 文件校验，未跳过 TLS 校验。证书到期为 2027 年 11 月 9 日 12:13:43，续期时保留可信 CA 或同步更新信任配置。
- age 解密身份只在维护者开发机使用，公钥与 worker 配置一致，并完成真实解密。私钥没有上传业务或存储主机。
- 独立恢复材料位于维护者侧 `~/.local/share/micro-one-api/backup-recovery/offsite-20261109/`，目录 0700、凭据及归档 0600；包含加密生产归档、外部索引/receipt、原版数据库镜像、worker、CA 与存储凭据。这里不保留解密后的业务 SQL。
- 前端宿主机版本另行核对：当前 index 的修改时间早于生产快照，实际 Web 制品及摘要已存入异机 `/opt/micro-one-api-backup/recovery-assets/` 并保留独立副本。前两份归档没有内嵌 Web index；本次随后为 worker 增加实时 `web/index.html` 配置项，后续归档自动登记该版本引用。未来发布前端时，按对应 index 版本保留可恢复制品。
- 旧同机存储及其配置仍保留；直连切换前的 worker 配置/env 位于业务主机 `/opt/micro-one-api/backup-storage/n2-before-offsite-20261109/`，目录及文件按私有权限保管。

## 恢复验证的实际边界

恢复只启动本机独立 MySQL/Redis，端口仅绑定 loopback，禁用桥接出口 masquerading；没有启动恢复的业务服务或连接上游供应商。MySQL event scheduler 保持关闭，Redis AOF 关闭。原服务运行账号与实际授权在隔离目标重建，再调用现有 testutil 装配的真实 owner。

| 核对项 | 证据及边界 |
| --- | --- |
| SQL 数据 | 从已认证归档提取 INSERT 内容，与恢复后相同版本 `mysqldump` 输出按 schema/表比较：88 表一致、0 差异；此检查在隔离夹具写入之前执行 |
| 配置密钥 | 归档内 JWT、Token hash、MFA、渠道密钥及用于本轮登录的配置，与实际容器配置逐项一致；报告不保存秘密值 |
| Token/会话 | 19 条原 Token 记录被完整恢复，原管理员会话由恢复 owner 验证有效；另用恢复密钥生成并验证隔离 API Key。既有 API Key 明文不在存储中，本轮没有重放用户原 API Key 明文 |
| 渠道 | 3 份真实渠道凭据均经原加密库和渠道 owner 解密，内容一致；没有执行上游请求 |
| MFA | 原快照 MFA 行为 0；在恢复副本使用归档密钥完成 enrollment、密文解密、TOTP、恢复码及重放拒绝。不是生产用户 enrollment，也不替代 N3 的真实验证器验收 |
| 钱包/预扣 | 原快照正金额 reserved 行为 0；在恢复钱包上通过真实 Billing owner 创建 100 quota 预扣，重复请求返回同一 reservation，重复释放后余额精确回到原值。夹具只写隔离副本 |
| 备份任务 | 恢复出来的历史 running 任务沿 repo 记为 uncertain，没有在恢复环境重新备份或清理历史归档 |

恢复导入耗时 **21.184 秒**；从独立下载开始到 owner 核对完成 **732.110 秒（约 12 分 12 秒）**，包含镜像准备及恢复环境配置重试。核对完成时快照年龄约 18 分 31 秒。该结果证明本次生产规模下的单次恢复，不表示已经验收持续生产恢复 SLA 或切流。

本机首次使用 `internal: true` 时没有分配宿主机端口，连接在导入前失败；改为仅 loopback 发布且禁用出口 masquerading，端口断言通过。恢复重启 Redis 后，其动态宿主机端口发生变化，重新查询实际端口后 owner 检查通过。均为一次性恢复环境配置问题，未修改业务实现。

## 同槽空闲轮询 409 修复

首个日槽成功后，生产 worker 的后续空闲轮询持续返回 409，心跳新鲜但状态为 attention；异机 `catalog` 正常，两份成功归档未受损。隔离 MySQL 在成功、失败、不确定三种已记录状态下均复现同槽再次领取 409。

`xdb` 统一开启 MySQL `clientFoundRows`，使 no-op upsert 也返回 1；原 `Claim` 误当作插入新任务，随后对已完成任务执行 queued→running CAS，自然冲突。该连接语义见 [MySQL 官方说明](https://dev.mysql.com/doc/refman/8.0/en/insert-on-duplicate.html)。保留全局连接设置，在已有 policy `FOR UPDATE` 锁内先查日槽，已存在即空闲返回，只为新槽插入任务。

新增 SQLite/MySQL/PostgreSQL 共 9 项存储边界回归，覆盖已成功/失败/不确定状态、同槽连续轮询及下一槽可领取；MySQL 显式通过真实 `xdb` 连接工厂重开隔离 schema。原 SQLite 测试无法暴露该 MySQL 语义差异，既有三库夹具直接建连接也不会自动开启该选项。用既有三库备份控制脚本接入回归，防止以后只跑 SQLite。

修复提交为 `66709a1b`。本地三库专项带 race 通过（9/9、0 跳过），全仓 build/vet、Makefile 单元门禁、分层检查、Linux/amd64 静态构建及 Linux CLI 启动通过；OCR 对 repo 与测试脚本评审为 0 发现（测试文件按工具默认规则排除，另行自审）。只替换 host worker，切换前保留二进制/配置/provenance 回滚副本；新 worker 连续四次真实空闲轮询均 ready、心跳推进、无错误，原两份归档及摘要不变，未重放备份。

## 日调度、停写与下一次核验

策略通过真实管理员会话沿 owner 更新，revision **1 → 2**：

```json
{"enabled":true,"interval_hours":24,"retain":7,"rpo_hours":24,"rto_minutes":60}
```

调度使用 UTC 日槽；启用时立即执行当前槽，以后对应北京时间每天 08:00。首个日槽的真实 Docker die/start 事件跨度为 **27.106 秒**；首次手动任务只保留了采样，停写上界为 45 秒，两种测量不能混写为同一精度。调度期间 worker state 目录实测峰值 **332,160,365 字节（约 317 MiB）**，主机最小空闲空间约 29.8 GiB。

两次备份后原 10 个业务 writer 和 Redis 均运行，容器 ID/镜像未改变；MySQL/Redis 及有健康检查的 writer 正常，Admin/Relay `/healthz` 为 200，没有 `resume.json`。独立外部索引已核对两份归档，但其中只有一份属于日调度。

- [x] 首次手动备份和隔离恢复通过后启用日策略。
- [x] 当前日槽 `schedule-2-20766` 成功，异机 receipt 与 owner 摘要相符。
- [ ] 11 月 10 日 08:00 后核验 `schedule-2-20767` 实际完成、异机归档/receipt 可读回、writer 恢复、worker ready、无 uncertain/deleting 遗留。
- [ ] 核对成功备份年龄及持续 RPO，并将实际结果回填本记录、计划及 TODO；缺证据不勾选 N2 完成。

次日直接复用正常管理员入口读取 `/api/admin/backups`、`/api/admin/backup-policy`，在 host worker 的私有环境中运行 `-mode catalog`，再按恢复手册 `fetch` 核对对应 job 的 SHA-256。不能只看 systemd active、worker 心跳或对象列表。24 小时周期与 24 小时 RPO 没有额外执行余量，下一次任务执行期间也需核对上一成功快照年龄，不预先声称持续达标。

## 回退与未执行项

本机恢复项目、回归数据库及其三个测试卷已销毁；临时解密 SQL/配置、运行凭据和 SSH 隧道已清理。异机归档、恢复索引、维护者侧加密恢复包及私有核对 harness 保留，生产临时 operator/凭据转交文件已移除。

回退先经 owner/CAS 关闭调度，确认无 queued/running/deleting 任务和重启日志，再停 worker 并恢复切换前私有配置；保持已有异机归档和恢复身份。不要删除存储目录、运行 `down -v` 或用旧命名空间凭据处理新目标。

仅回退 worker 修复时，使用 `/opt/micro-one-api/backup-storage/n2-worker-fix-20261109/` 的二进制/配置/provenance 副本；它与前述 S3 切换前副本用途不同。旧 worker 会恢复本轮已经复现的同槽 409，不作为长期修复方案。

未执行生产覆盖恢复、生产 MFA enrollment、付费调用、PITR、后台 PostgreSQL/SQLite 备份或新的自动任务。worker 修复属于 PATCH：恢复既有日槽幂等语义，不新增 API 或备份契约，本轮未发版/tag。全仓 build/vet 已通过；额外 `go test ./...` 因未启动完整本地服务栈的 E2E 用例失败，不能记全仓测试通过。Makefile 定义的单元门禁已通过，三库专项与备份包 race 也已通过。没有前端代码变化，未新增桌面/移动 Playwright 验收。
