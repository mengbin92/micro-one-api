# Micro-One-API v0.37.1 发布：SSRF 旁路告警、上游错误缓冲与 Chat SSE 终止修复

> 2026-10-29 · 上一版：[v0.37.0](./release-v0.37.0.md)（2026-10-29）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.37.1)

v0.37.1 是 v0.37.0 之后的 **PATCH 修复版本**：修复 OCR scan 登记的三项存量安全与健壮性问题，恢复已有 SSRF 防护的可见性、错误响应的内存上限和流式请求的终止语义。没有新增功能、公开 API、配置项、依赖或数据库迁移，因此按 PATCH 发布。

**生产升级须先补既有 Log 145 迁移。** 该迁移属于 v0.37.0，现场尚未应用；本次先升级 Billing/Log owner，再更新全部使用共享 provider 的服务和账号 worker。Web、Identity、Config 不在本次部署范围；收费、强制认证与证据监控等开关保持现场状态。生产部署、机器评审和本版流水线结果以[本次运行记录](../runbooks/relay-safety-v0.37.1-2026-10-29.md)为准，进行中的验证不计为通过。

## 修复内容

### 1. SSRF 旁路改为共享边界单次安全告警（P1）

**根因**：`PROVIDER_DISABLE_SSRF_CHECK=true` 会在进程内关闭所有 provider 的 SSRF 校验，包括连接时的 DNS/IP 校验与重定向防护。原提示只在 Relay main，其他服务及直接校验路径可以静默使用全局旁路。

**修复**：在 factory、HTTP/SSE client 构造、URL 校验、dial 和 redirect 边界复用同一检查，每进程只记录一次 Warn 安全告警；日志未就绪时保留后续告警机会。删除 Relay main 的重复提示。沿用已有环境变量及日志阈值，不新增第二个旁路开关。

**影响服务**：`relay-gateway`、`admin-api`、`channel-service` 及复用其镜像的 `channel-account-ops-worker`、`monitor-worker`、`notify-worker`。生产继续保持该旁路关闭，告警不能代替 SSRF 防护。

**回归**：子进程隔离验证七个入口、单次告警和延迟日志初始化；原实现 `warnings = 0, want 1`，修复后通过。

### 2. 八条非流式路径按实际成功条件限制错误缓冲（P2）

**根因**：非流式路径先按 128MB 成功上限读取响应，再判断 HTTP 状态。上游供应商返回的大错误体因此可以放大进程内存；只修两个直接命中点仍会遗漏其他 adapter 的同类读取。

**修复**：八条非流式路径统一使用共享读取函数，由调用方按自身契约判断成功：typed Chat 仅 HTTP 200 成功，raw 为 HTTP 2xx 成功。成功响应继续使用 128MB 上限，错误响应统一限制为 1MB `MaxUpstreamErrorBody`；HTTP 状态和 Retry-After 保持原语义。已有流式错误读取继续使用 1MB 上限。

**影响服务**：执行上游请求或渠道探测的 `relay-gateway`、`admin-api`、`channel-service`、`monitor-worker`；共享 provider 的其他调用方随本次镜像更新一并覆盖。

**回归**：429 大错误体与 typed 201 错误原均缓冲 2097152 字节，期望为 1048576；修复后通过。raw 201 大响应仍完整返回，避免收紧错误上限时误截正常 2xx 响应。

### 3. Chat SSE 先结算再发送成功标记，统一失败出口（P3）

**根因**：typed Chat 在 commit 前发送 DONE，使结算失败看起来像成功；`StreamError` 与完成标记的判断顺序不一致，`InvalidPayload` 依赖生产者约定而被忽略，部分后置失败没有客户端可见的终止错误。

**修复**：

- 正常顺序统一为“内容交付 → commit → DONE → usage log”，用量日志 RPC 不延迟成功标记。
- 上游错误、解析拒绝或缺少完成标记时取消上游、释放预扣；客户端连接可写时发送一次脱敏 SSE error，不发 DONE、不重试。
- commit 失败或结果不确定时保留 reservation，不 release、不重试，避免 Billing 已收到提交后再退款或重复结算；返回已转发中断标记，外层不再追加 JSON。
- 客户端取消或内容写失败时取消上游并释放预扣，不再尝试错误事件。DONE 写失败发生在内容交付及结算之后，保留已提交结算和用量日志。
- 档位 usage 缺失或无效仍按原契约释放预扣，不恢复估算收费；其他 raw/SSE 协议的终止策略保持原边界。

**影响服务**：`relay-gateway`。Billing/Log owner 本次先行升级，以满足当前 Relay 的既有协议和日志字段要求。

**回归**：真实 provider → HTTP 链路的六个原失败场景转绿，覆盖错误与完成标记并存、InvalidPayload、未完成流、commit 失败、正常用量、取消及写失败；专项同时复跑档位结算测试。

## 兼容性说明

- **API**：无新增 RPC、请求字段或路由。typed Chat SSE 的异常流现在明确发送脱敏错误事件且不发送 DONE，调用方应按错误终止处理，不能把已收到内容等同于完整成功。
- **数据库**：无新增迁移文件。生产 Log 尚缺 v0.37.0 的 `145_add_log_service_tier`，须在新 Log/Relay 启动前补齐；Identity 143、Billing 144 已登记应用，其余 owner 迁移与当前 manifest 一致。
- **配置与依赖**：无新增配置项或依赖。`PROVIDER_DISABLE_SSRF_CHECK` 仍须在生产保持未设置或关闭；Warn 遵循既有日志级别。
- **资金与历史记录**：不重算历史账单；commit 结果不确定时不补造成功、不释放原 reservation，也不自动重放。已有档位快照和资金 owner 的结算契约继续生效。
- **范围**：七个镜像为 Billing、Log、Channel、Monitor、Notify、Relay、Admin，账号 worker 复用 Channel 镜像；Web、Identity、Config 不在本次部署范围。真实收费、外部认证因子、支付和监控启用不因本次发布而通过或扩大。

## 升级步骤

1. 核对版本、运行镜像及现有配置，备份 Log 数据库；为目标容器的旧镜像保留回滚标签和 Compose 备份。凭据和用户数据只在私有环境使用，不写入发布证据。
2. 使用现有迁移 CLI 按 Log ownership 仅应用既有 145；再次执行应无操作。以 Log 实际运行账号验证三个新列可读及表级 SELECT/INSERT/UPDATE 权限。
3. 在本机交叉构建并上传 Linux/amd64 镜像，生产主机不构建。先更新 Billing/Log，再更新 Channel、账号 worker、Monitor、Notify、Relay 和 Admin 后端。
4. 单独核对 `channel-account-ops-worker` 的显式 image 字段；现场 alias 独立钉旧镜像，`extends` 不会自动覆盖。保留其旧镜像和 Compose 备份，再切到与 Channel 主服务相同的镜像并验证健康。
5. 保持收费、强制认证及证据监控等开关的现场状态。核对源码摘要、镜像、健康、重启次数、SSRF 设置和日志分类计数；验证匿名拒绝及已认证 Channel、Log、Billing 和 Identity 只读链路，不调用付费推理、不创建支付订单。
6. 将实际部署、验收和评审证据回填运行记录后，按 develop → main → tag 顺序发布；本版 CI、Release E2E 和 amd64 构建须取得独立结果。

### 回滚限制

- 保留 Log 145 的新增列和迁移记录，不通过删字段或恢复旧数据库快照回滚镜像。
- 发生 commit 结果不确定时交由既有 Billing owner 核实，不因回滚而 release 或重放 reservation；保留冻结快照与历史账单。
- 当前强制认证、档位计价及支付配置仍按此前版本的独立启用和回滚约束处理；本次不降低既有认证要求。

## 验证

| 状态 | 证据与边界 |
| --- | --- |
| ✅ | `make test-unit`、`make test-race`、provider 专项 race、`go build ./...`、`go vet ./...` 通过；三项修复均有针对原失败症状的红绿回归 |
| ✅ | format、architecture、migration、部署文档检查通过；RBAC 915 条及对应 handler 源码摘要已同步，IAM browser result 检查通过 |
| ✅ | Web lint、666 项单测、196 项 critical 和生产构建通过；critical 行覆盖 93.34%、分支覆盖 85.99%。本次不部署 Web |
| ✅ | 独立复核发现的 typed 201 成功边界已修复，并补充 raw 201 大响应不被截断的回归 |
| ⏳ | 最终 OCR high 机器评审仍在进行，结果待回填；独立复核不代替该门禁 |
| ⏳ | 桌面/移动浏览器最终验收待回填；已中断的进程不计为通过，单元测试和 IAM result 检查不代替真实浏览器结果 |
| ✅ | 生产只读基线：十个业务容器健康、重启零次、SSRF 旁路均关闭；现存精确登录凭据可完成 Channel、Log、Billing 与 Identity 读取，匿名请求拒绝。此为升级前基线，不代替部署后验收 |
| ⏳ | Log 145、七镜像及账号 worker 的生产升级和升级后验收正在进行，结果待回填[运行记录](../runbooks/relay-safety-v0.37.1-2026-10-29.md) |
| ⏳ | 本版 tag 对应 CI、Release E2E、amd64 镜像和 GitHub Release 尚待发布流程取得结果 |

发版前已扫描八条既有 `shortcut:` / `ponytail:` 记录，本次未新增捷径；既有边界见运行记录。本次未执行付费推理、真实支付或外部认证因子验收。

## 完整变更日志

以下仅列本次发布的计划提交，提交后按最终 subject 核对；不重复 v0.37.0 的提交。

- `fix(provider): warn once when SSRF protection is disabled`
- `fix(provider): bound non-streaming upstream error bodies`
- `fix(relay): settle chat streams before their success marker`
- `docs(release): v0.37.1`
