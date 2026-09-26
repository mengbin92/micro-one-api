# Micro-One-API v0.32.2 发布：多副本凭证协调、控制台验收与企业微信告警配置

> 2026-09-26 · 上一版：[v0.32.1](./release-v0.32.1.md)（2026-09-23）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.32.2)

v0.32.2 是 v0.32.1 之后的 **PATCH 运行可靠性与验收收口版本**：保护多 Relay
OAuth 轮换免于重复刷新，补齐控制台历史快照及验证门禁，并让 Alertmanager 通知
使用已有企业微信机器人发送格式。通知配置修复已于 2026-09-26 更新生产
`notify-worker`；真实外部 firing/resolved 送达仍待验收。

**无新增公共 HTTP API**。内部 channel gRPC 契约增量扩展，三方言新增 channel
迁移 109。`ALERTMANAGER_NOTIFY_TYPE` 可选、默认 `webhook`；使用企业微信机器
人时需提供已有的 `NOTIFY_WECOM_WEBHOOK_URL`。前端产物独立于 admin 镜像。

## 修复与改进

### 1. 多 Relay OAuth 凭证轮换协调（D1）

**根因**：单实例凭证缓存与刷新时序不能保证多 Relay 只刷新一次。租约失效、
进程退出或 OAuth 回包不明时，旧 refresh token 可能被重复使用，迟到写入也可能
覆盖重新授权。

**修复**：协调模式每次 OAuth 执行从 channel 权威存储回读；Redis 租约负责准入，
数据库 revision 与 `credential_refresh_pending` 负责持久占用及陈旧写入保护。
新增 claim RPC、迁移 109、冷账号扫描与降级告警；协调模式缺 Redis 或后台
sweep 时拒绝启动。默认单实例模式保持原行为。

**影响服务**：`relay-gateway`、`channel-service`、`identity-service`。生产已于
2026-09-23 应用迁移 109 并启用 Relay Redis 协调；当前仍为单 Relay、单 channel，
且只有静态账号，真实供应商轮换与多副本故障切换仍待外部证据。详见
[D1 运维手册](../runbooks/subscription-redis-multi-replica-runbook.md)。

### 2. 控制台状态与可复现验收（Q1–Q3）

**根因**：Playground 多轮请求的历史检查器可能读取当前轮状态，充值页主题与
键盘状态尚未收口；raw、历史迁移及性能判断缺少当前提交可复现门禁。

**修复**：每轮保存独立请求快照，充值页使用主题 token 与键盘语义；补 raw
默认模型/映射/预留/失败释放 E2E、历史 schema 语义、PR 按路径触发的 E2E 和
事故模板。构建加入 JS/CSS/字体字节预算，性能脚本要求三次样本取中位数，并
用相同 fixture 对照 Dashboard 索引。

**影响服务**：`admin-api` 挂载的 `web/dist`、测试及 CI。生产前端已于
2026-09-24 单独更新；本版通知部署不重建前端。支付宝 F17 沙箱往返、当前提交
Linux/amd64 三次全量 k6 与生产 MySQL 同负载 `EXPLAIN ANALYZE` 仍待执行。
详见[第三批验收记录](../runbooks/third-batch-q-acceptance-2026-09-23.md)。

### 3. Alertmanager 适配企业微信机器人（O2）

**根因**：Alertmanager 告警组固定创建为通用 `webhook` 通知，企业微信机器人
要求 `msgtype=text` 格式；生产 Compose 未传递类型配置，已有机器人地址无法
用于告警组投递。

**修复**：新增可配置的告警组通知类型，在配置加载时拒绝无效类型及需要逐条
收件人的 `email`；三套仓库 Compose 模板传递类型和各发送端地址。回归测试验证
firing/resolved 以企业微信格式送达本地 HTTP 接收端，并持久化 `sent`。生产
独立 Compose 仅增补这一变量，不覆盖其余定制配置。

**影响服务**：`notify-worker`。2026-09-26 本机交叉构建 linux/amd64 镜像后
上线；镜像、回滚标签、脱敏配置及健康证据见[部署记录](../runbooks/evidence/notify-v0.32.2-deploy-2026-09-26.json)。

## 兼容性说明

- 迁移 109 对 MySQL/PostgreSQL/SQLite 增加 `credential_refresh_pending`，为增量
  字段；内部 claim RPC 仅供服务间使用。旧静态账号仍保持静态凭证语义。
- `ALERTMANAGER_NOTIFY_TYPE` 不配置时默认 `webhook`。可选 `event`、`wecom`、
  `dingtalk`、`feishu`、`slack`；`email` 不接受空收件人，因此不支持作为告警组
  类型。接收端地址未配置时通知明确失败，不将入队当作送达。
- 现有账务、executor 默认开关和公共协议不变。当前生产只有一个 Relay 和一个
  channel；多 channel selector/会话协调仍未实现。

## 升级步骤

1. 从 v0.32.1 升级的环境先备份配置和镜像，按
   [D1 运维手册](../runbooks/subscription-redis-multi-replica-runbook.md)在 channel
   应用迁移 109，确认凭证待补写状态，再更新 channel、Relay、identity。生产已
   于 2026-09-23 完成这一步；无需重复发布已运行的等价镜像。
2. 若需使用本版控制台，单独构建并发布宿主机挂载的 `web/dist`；admin 镜像
   更新不会替换前端。生产已于 2026-09-24 更新该静态目录。
3. 在通知环境配置 `ALERTMANAGER_NOTIFY_TYPE=wecom` 和
   `NOTIFY_WECOM_WEBHOOK_URL`，本机交叉构建 linux/amd64 `notify-worker` 镜像、
   传到 Linux 主机并重建该容器。独立部署的 Compose 只需给 notify-worker
   透传类型变量，保留现有源文件定制；不要在生产主机构建镜像。
4. 核对 notify-worker 的镜像、健康、重启次数、实际类型及接收地址是否存在。
   经授权发送真实告警时，再用外部群回执和 `sent` 记录验收 firing/resolved。
   回滚时恢复 `rollback-20260926-100806` 镜像和对应 Compose/.env 备份；迁移
   109 为增量字段，不删除已有凭证状态。

## 验证

- `make verify` 通过：Go 单测与关键包 race、分层、迁移检查、生成 API 类型、
  前端 lint、196 项单测和生产构建字节预算均通过；`make test-integration` 通过。
- notify 全包测试覆盖配置类型、企业微信 firing/resolved 格式、真实本地 HTTP
  接收与持久化 `sent`；三套仓库 Compose 模板解析出指定类型与接收地址。
- 生产 notify-worker 使用 linux/amd64 镜像
  `sha256:60fe97072e3d518f34833e9b999fd6f8c79b4a7cf7f6c164dfbc6dbd13a30696`，
  运行中、重启次数 0、`/healthz` 返回 200；类型为 `wecom` 且接收地址已注入。
- 本次未主动向外部企业微信群发送测试告警。真实 firing/resolved 回执与对应
  `sent` 记录、OTel 导出、生产缓存成本、支付宝沙箱和性能对照仍按
  [下一阶段待办](../design/next-stage-todo-2026-09-26.md)继续验收。

## 完整变更日志

- `feb4241f` feat(credential): coordinate replica refresh and deploy D1
- `e3ee73d4` feat: complete third-batch console and quality gates
- `537932cc` feat(notify): make alertmanager notification type configurable
- docs(release): v0.32.2
