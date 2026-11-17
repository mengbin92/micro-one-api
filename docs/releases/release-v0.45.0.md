# Micro-One API v0.45.0 发布：独立上游 usage 留证与异常告警

> 2026-11-17 · 上一版：[v0.44.1](./release-v0.44.1.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.45.0)

本版为 **MINOR 功能版本**：新增与财务结算解耦的观察事件、可靠投递、Log 内部查询契约、迁移 152 和异常告警。观察不触发第二次推理，不修改用户价格、原生结算名单或历史账本。新 producer 默认关闭，生产升级保留现场开关；部署健康不代表 48h 验收或收费晋级通过。

## 1. 用户请求独立留证

**根因**：原 shadow 主要输出日志，财务 durable 名单为空时，普通用户请求没有独立持久证据。

**实现**：原始响应转换前保存 started/checkpoint/terminal；HTTP redirect 分跳与 WS 每轮绑定真实请求及来源。请求级 not_sent/unmatched 不伪造 send，事件摘要和身份严格校验；SQLite outbox 先落盘、Log 持久 ACK 后清 payload，关闭采集仍排空旧事件。有限告警元数据可在 ACK 后重启恢复，坏行隔离不阻塞正常投递。

**影响服务**：relay-gateway、log-service。

## 2. 权限、存储和恢复边界

**根因**：详情复用列表权限、弱游标、计时开关影响归属和字符级容量统计，无法满足严格留证。

**修复**：列表只返回摘要；详情要求 read 与 content 的完整范围交集；毫秒窗口、严格游标、来源灰度与身份独立。容量按 UTF-8 字节、共享磁盘余量和数据库上限控制。152 使用真实分页/清理索引，MySQL 身份二进制排序、索引原子建表。真实 PostgreSQL 验证进一步发现 bool 与整数列不兼容，PO 改用仓库已有 xdb.Flag，三库回归通过。

**影响服务**：log-service、relay-gateway；迁移 owner 为 Log。

## 3. 告警与部署

**根因**：首次非零 counter、多实例缺指标和慢发送会掩盖告警；新 bind 目录未预创建会导致低权限进程无法启动。

**修复**：独立发送分母、首次事件时间戳与恢复、逐实例预期状态/队列新鲜度告警；统计独立可取消并 recover/join。脚本先准备 0700、65534:65534 的私有目录，失败在构建/重启前停止。保留既有上游供应商账单扫描停用决定。

**影响服务**：relay-gateway、log-service、Prometheus 配置；无前端变更。

## 兼容性说明

- 新增 Log 内部 Record/List/GetUsageObservation，专用系统能力写入、独立用户权限查询；无新增公开 HTTP 页面。
- 先应用 Log 152 并确认运行权限，再更新消费者与 producer。已经部分执行过旧 152 的环境须先核对实际 schema，不删除 marker 盲目重放。
- 新观察配置和财务名单完全独立。96 MiB DB 上限、64 MiB pending payload、七天墓碑需要开窗前按流量核预算；达到边界明确降级，不能承诺无限容量。
- 只验证实际 RoundTrip/WS create；不将 Go 内部隐式重发/TCP 重传等同于已验收的业务发送。

## 升级步骤

1. 从 .env 读取部署目标，保存镜像回滚标签及 Compose/规则备份。
2. 备份 Log schema，通过正式 owner Runner 应用 152、确认 SELECT/INSERT/DELETE 最小运行权限；既有缺失的 Log 150 由同一 Runner 补齐。
3. 本机 linux/amd64 构建，生产仅加载；准备两个私有 outbox 目录，先更新 Log、再 Relay，不重发前端。
4. 保留现场观察/收费/探测开关。按运行手册另行固定观察范围、性能预算及窗口；不自动开启全量观察或付费探测。
5. 验证健康、重启、源码摘要、表结构/迁移 marker、运行权限及配置；通过后走 develop → main → tag。

**回滚**：恢复部署前保存的镜像和配置。若已开启新采集，先关闭 producer，保留新消费者和兼容 worker 排空；不删除 pending、墓碑、152 或历史证据。观察开关未开启时可直接回退本轮服务镜像。

## 验证

- [x] 完整 make verify：Go 单测/race、生成、分层、迁移、RBAC、前端 lint/test/critical/build 与预算。
- [x] SQLite/MySQL/PostgreSQL 正式 Runner 与观察存储专项 race；PostgreSQL 整数标志修复后复跑通过。
- [x] 八组 promtool、12 项部署脚本回归及低权限目录隔离验证通过。
- [x] OCR delegate 宿主复核与最终源码摘要核对；外部 LLM OCR 仍 partial，其十条建议已逐条处置，不冒充外部全量通过。
- [x] 最终生产 Log/Relay 镜像健康 200，重启、panic、ERROR 均为 0；迁移 152 marker=1，新表最小运行权限已配置，私有卷已接入；11 条观察规则 health=ok/state=inactive，原费用对照规则仍未加载。新 producer 保持 false。[脱敏证据](../runbooks/evidence/release-v0.45.0-production.json)。
- [ ] linux/amd64 性能预算、生产通知真实收件和新的完整 48h 窗口；不属于本次部署健康证明。

## 完整变更日志

- feat(log): add independent durable usage observation storage
- feat(relay): wire observation-only upstream evidence across execution paths
- feat(ops): add usage observation alerts and safe deployment support
- docs(release): v0.45.0
- fix(log): reject overflowing observation HTTP status
