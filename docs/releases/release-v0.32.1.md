# Micro-One-API v0.32.1 发布：流式取消终态、Playground 安全头与 SQLite 分组修复

> 2026-09-23 · 上一版：[v0.32.0](./release-v0.32.0.md)（2026-09-23）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.32.1)

v0.32.1 是 v0.32.0 之后的 **PATCH 可靠性与安全修复版本**：修复取消请求释放
预留后仍被审计为成功的问题，为管理台页面接入 CSP 和安全响应头，并消除
SQLite 并发写入时创建路由分组的快照锁冲突。

**无公共 API/proto 变更、无新增数据库迁移**。受影响服务为 `channel-service`、
`admin-api`、`relay-gateway`。本版无前端运行时代码变更，无需重新发布 `web/dist`。

## 修复内容

### 1. 流式取消、超时和异常断流的终态

**根因**：legacy 流中断分支释放预留后返回 `nil`，重试执行器据此将路由审计
记为成功；固定的 `stream_error` 指标也掩盖了取消原因。orchestrator 的中断
结算同样丢失了请求取消与超时的区别。

**修复**：普通渠道 Chat/Messages/Responses 在响应开始后发生中断时返回
不可重试错误，保留取消原因，停止后续重试及追加 JSON 错误。客户端取消记录
`canceled`，请求预算超时记录 `timeout`；orchestrator 结算保留最终来源和
回退信息。Chat 在账务清理前取消上游流，避免清理 RPC 等待延迟上游中止。

**影响服务**：`relay-gateway`。预留释放和计费语义保持不变；已开始的 SSE
可能保留 HTTP 200，应通过最终审计和执行指标判断是否取消。

### 2. Playground 与管理台安全响应头

**根因**：admin 静态页面没有接入现有安全头中间件，Playground 的跨域 Relay
连接来源也没有随实际配置收敛到 CSP。

**修复**：SPA 页面与静态资源复用安全头；页面 `connect-src` 允许同源及
`ServerAddress` 指定的 Relay origin，未配置时可用 `ADMIN_WEB_RELAY_ORIGIN`
匹配前端构建的回退地址。拒绝非法地址，脚本保持同源，兼容现有图表样式属性。
新增隔离浏览器验证脚本，检查 CSP、跨域请求、请求 ID 和 API Key 不落存储。

**影响服务**：`admin-api`。CSP 由服务端响应提供，更新 admin 镜像即可生效。

### 3. SQLite 并发创建路由分组

**根因**：事务内先查询分组 key、再插入的流程会在并发 WAL 写入后持有旧读
快照，插入升级为写事务时触发数据库锁错误，管理接口返回 503。

**修复**：通过既有唯一键约束处理重复 key，移除事务中的前置查询，并增加
并发写入回归测试。

**影响服务**：`channel-service`，主要影响使用 SQLite 的部署。

## 兼容性说明

- 无新增 DDL、proto 或必填配置；分组唯一键和现有重复键错误契约保持不变。
- 路由终态增加 `canceled` / `timeout` 标签。外部报表如枚举结果，需纳入这两项；
  取消不再计入成功，指标不会因此产生用户或请求级高基数标签。
- `ADMIN_WEB_RELAY_ORIGIN` 为可选回退配置。优先读取 `ServerAddress`，跨域
  Playground 部署应核对该地址与页面 CSP，配置变更后重新加载页面。
- 不改变 executor 默认开关、计费价格或额度策略。第三批其余 Q1–Q3 工作及
  第二批真实通知、OTLP 导出和性能验收仍按[当前计划](../design/next-stage-plan-2026-09-22.md)推进。

## 升级步骤

1. 保存受影响服务的当前镜像和配置，核对生产状态；本版没有新数据库迁移。
   重启 Relay 前按既有流程检查凭证待补写和在途请求。
2. 使用发布镜像或在本机交叉构建并更新 `channel-service`、`admin-api`、
   `relay-gateway`，不要在生产主机构建。自建流程可执行：

   ```bash
   git fetch --tags
   git checkout v0.32.1
   ./scripts/deploy-update.sh channel-service admin-api relay-gateway
   ```

3. 核对 `/healthz`、管理台 CSP 与 Relay origin，验证 SQLite 创建分组；用
   短期限额令牌执行一次流式取消，按根请求 ID 回读 `canceled` 审计、`released`
   预留及零消费，步骤见[取消验收手册](../runbooks/routing-observability-runbook.md#流式取消验收)。
4. 本轮 admin/relay 修复已分批部署并验证，现网应按镜像与实际状态核对是否
   需要更新；发布标签本身不触发生产容器重启。回滚恢复对应旧镜像和配置，
   保留预留、账本与审计记录。

## 验证

- 发布候选 `make verify` 通过，覆盖 Go 单测、race、分层、迁移治理、生成
  前端 API 类型一致性，以及前端 lint、单测和生产构建。
- `make test-integration`、197 份 Markdown 本地链接检查及 diff 空白检查通过。
- 相关 Go 包回归及取消/断流三轮 `-race` 通过；10 个真实 HTTP 场景覆盖
  HTTP/1.1、HTTP/2、legacy/orchestrator 和请求预算超时，确认受控上游收到
  取消，预留只释放一次、不提交、不重试，审计与指标终态一致。
- 安全头单元测试与隔离浏览器检查通过。正式 Playground 的停止按钮在修复
  前已确认请求 `net::ERR_ABORTED`、无 CSP 异常或 API Key 落入浏览器存储；
  取消修复上线后复验为真实 API 调用，未重复浏览器测试。
- 渠道 9 `step-3.7-flash` 的 Messages 和 Chat 请求分别在请求开始后 837ms、381ms
  收到首帧并取消。两者均一次尝试、预留 `released`、实际费用 0、无消费分录，最终路由
  审计为 `canceled`，对应执行指标各增加 1；测试令牌已停用。镜像、回滚标签、
  请求及预留 ID 见[脱敏验收证据](../runbooks/evidence/next-stage-channel9-cancel-2026-09-23.json)。
- 上游请求取消已由受控 HTTP 服务确认。StepFun 内部是否停止推理仍缺少供应商
  逐请求回执；HTTP/2 可复用连接保持连接状态不能证明取消失败。
- 标签触发既有 Release 流水线，依次执行 MySQL/SQLite 路由、Compose E2E 和
  管理台浏览器门禁，再构建九个服务的 linux/amd64、linux/arm64 镜像并创建
  GitHub Release。

## 完整变更日志

- `6b228152` fix(routing): avoid SQLite snapshot lock on group creation
- `35ad67d1` fix(playground): secure pages and preserve relay cancellation outcomes
- docs(release): v0.32.1
