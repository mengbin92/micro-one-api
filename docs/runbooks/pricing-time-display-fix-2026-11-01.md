# 用户价格页忙闲时价格展示修复

2026-11-01（北京时间）。关联 [B1 计价契约](../design/b1-context-time-pricing-contract-2026-10-29.md)。

## 现象与根因

生产只读检查确认：`deepseek-flash` 存在于 `/api/pricing`，响应仅包含普通输入、输出、缓存读取单价，没有 `time_pricing`。本地接口回归使用已启用且通过 Billing 校验的完整分时配置，同样断言失败；页面测试即使收到完整分时数据，也找不到“忙时”。

同时从运行中 admin 服务的连接信息定位实际 owner 数据库，只读确认生产配置 `enabled=true`、目标模型为 `deepseek-flash`、包含 `time_pricing`、时区为 `Asia/Shanghai`，排除了未启用或配置写错库。

根因有两处：用户价格接口未投影 `ServiceTierPricing` 的客户售价；页面只读取普通单价。另在联动测试中发现，公共价格请求继承授权刷新使用的取消信号，会被授权状态变化取消；档位配置保存后也没有刷新公共价格缓存。

## 修复范围

- biz 从已有配置 owner 读取、复用 Billing 校验，按字段白名单构造客户售价 DO。该 DO 不含采购价格、来源类型和内部来源 ID。
- Admin 接口将启用的档位、忙闲时段及可组合的长上下文售价转换为 USD/1M tokens，继续通过公开且启用的模型目录过滤。分时配置可以独立提供模型价格；合并时按目录相同的大小写规则去重，避免旧普通价格覆盖分时投影；关闭配置保持原普通价格路径。读取或校验失败返回错误，不冒充空配置。
- Web 按忙时/闲时和上下文条件列价，相同售价的服务档位合并显示，不同售价分别显示；展示时区、周内窗口、节假日、日历有效期及预扣时段规则。明确零价与未配置价格的区别。
- `/pricing` 和 Tokens 模型列表的公共价格读取使用 Query 自身的取消信号。管理员保存档位配置成功后，使公共价格缓存失效；读回失败也会触发公开接口重新读取 owner，不展示未确认草稿。

涉及 admin-api 与 Web。没有数据库迁移，不修改 Billing/Relay 收费逻辑、生产配置和历史账单。

## 验证

| 项目 | 命令 / 证据 | 状态 |
| --- | --- | --- |
| 原始复现 | `go test ./app/admin/internal/server -run '^TestReadonlyPricingIncludesEnabledTimePricing$' -count=1` | 修复前缺少 `time_pricing`，修复后通过 |
| Go 回归 | `go test ./app/admin/internal/server ./app/admin/internal/biz ./app/admin/internal/data ./app/admin/internal/service` | 通过 |
| Go 静态检查 | `go vet ./app/admin/internal/biz ./app/admin/internal/data ./app/admin/internal/service ./app/admin/internal/server` | 通过 |
| Web 回归 | `cd web && npm test -- src/pages/PricingPage.test.tsx src/pages/admin/PricingPage.test.tsx src/pages/TokensPage.test.tsx src/lib/service-tier-pricing.test.ts src/components/admin/ServiceTierPricingEditor.test.tsx` | 89 个测试通过 |
| Web 静态检查 | 改动 TS/TSX 的 ESLint；`cd web && npx tsc -b --noEmit` | 通过 |
| 浏览器验收 | `cd web && npx playwright test e2e/pricing-time.spec.ts` | 桌面/手机 × 中文/英文，4 个场景通过；价格横向滚动可见，节假日展开可用，无页面横向溢出 |
| 数据边界 | 嵌套采购字段全部排除；未公开模型不展示；关闭/错误配置；独立档位价格；缺失缓存创建价格；严格上下文边界；显式零价；大小写模型别名；英文无中文残留 | 回归通过 |
| OCR | `ocr review --audience agent --effort high --format json` | 完整完成 9 个生产文件；建议逐项复核，无 P0/P1 阻断项；见下方评审记录 |
| 完整门禁 | `make all`、`go build ./...`、`go vet ./...`、`make verify` | 全部通过；已补齐新 SOURCE 登记和来源摘要 |
| 生产构建与部署 | Web 生产构建已通过；镜像部署未执行 | 按后续指令先合入 develop，再部署与发版 |

## 机器评审复核

第一轮超过预算后停止，未标记通过；第二轮完整完成。翻译、保存后缓存失效及分组规则说明已处理；未采纳纯抽象建议。保持既有三项价格展示范围，公开 API 已包含五桶售价；不按三桶值误合并不同完整售价。逐项结论及验证状态见[评审证据](evidence/pricing-time-display-review-2026-11-01.json)。

## 全量门禁补齐（2026-11-01）

后续部署授权后执行完整门禁，发现新增 `readonly_pricing.go` 尚未登记到 RBAC 来源矩阵。最小复现 `go test ./cmd/rbac-contract-check -run '^TestReviewedSourceAndNegativeDrift$' -count=1` 报清单大小不一致；实际 inventory 比矩阵恰好多一条 SOURCE，无新增 HTTP/RPC 入口。按真实只读边界登记新文件并刷新两个既有来源摘要后，原用例及完整 `make verify` 均通过。

合入前追加分支差异机器评审完整完成 10 个文件，无 P0/P1 阻断项；唯一英文计数措辞建议已处理。另修复历史阶段计划提交缺少正文导致的 Backend CI 失败：仅补提交说明、文件树一致，保留旧 ref 并用绑定旧 SHA 的 lease 更新 develop；[新 CI 全部通过](https://github.com/mengbin92/micro-one-api/actions/runs/41262944157)。

## 发布要求

本次属于 PATCH：补齐已上线计价能力的用户展示，不改变计费契约。发布需要同时更新 admin-api 与宿主机挂载的 Web dist；只替换其中一个不能完成修复。上线前补齐构建/PJR 与 OCR 门禁，并保留旧镜像和 Web dist 回滚备份。

部署后检查 `/api/pricing` 的目标模型包含 `service_tier_prices`、`time_pricing`，响应中不出现采购字段和内部来源信息；再在桌面及手机访问 `/pricing` 核对实际配置的两个时段价格和规则。
