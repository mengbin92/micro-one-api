# DeepSeek Flash 启用状态修复

2026-10-15（Asia/Shanghai）。用户要求修复线上 `deepseek-flash` 一直无法启用的问题，并提供已登录管理员页面完成正常管理接口操作。

## 根因

模型 519 的目录开关 `status=1`、`is_public=1` 已保存，但其唯一渠道映射 256（渠道 11，ds）仍为 `enabled=0`。渠道自身启用、healthy、无熔断，最近库存包含 `deepseek-flash`。没有其他已启用的渠道或订阅映射，因此 `EffectiveStatus()` 返回 0。

该映射由[受控库存发现](deepseek-catalog-repair-2026-10-14.md)默认禁用登记；目录启用不会连带修改来源映射。旧详情页只能查看映射，渠道模型编辑器又保留非托管映射状态，缺少可完成授权的页面入口。目录把这个状态直接标成“禁用”，加重了误解。

## 线上修复及验证

北京时间 16:07:40，在现有 actor 1 的 IAM 会话中确认 `channel.model_mapping.update` 权限后，调用 POST `/api/admin/channels/11/models`，提交模型 519、`enabled=true`、目录当前 `authorization_revision=7` 对应的 `expected_model_revision`，并保留 priority=0、config=`{}`、upstream_model_id=`deepseek-flash`。请求包含变更原因；接口返回 HTTP 200、success=true。没有直接改库。

只读核验确认映射 256 已启用，模型授权版本 7→8，`resource_write_audits` 留有 actor 1 / `channel.model_mapping.update` / resource 256 / success 及变更原因。目录接口复查为 configured_status=1、status=1、channel_count=1。刷新真实管理员页面后显示供应商 ds、状态“启用”、渠道数 1，原可用性检查由 FAIL 变为 PASS。

上述映射修复没有修改价格、凭据、其他模型或映射，没有发起付费推理。后续价格发布见下文。

## 模型管理前端修复（已发布）

- 列表和详情对 configured_status=1、status=0 显示“已启用，暂无可用来源”，详情说明需要检查映射及其所属来源的启用状态。
- 详情页新增现有渠道/订阅映射启停按钮，复用既有管理接口与 `channel.model_mapping.update` 权限。保留映射配置、优先级、上游模型 ID 和订阅分组。
- 写入使用详情返回的 `authorization_revision`，沿用管理请求的原因输入；成功刷新详情和列表，409 刷新版本但不自动重试。库存发现仍默认禁用新映射。

回归先复现旧页面失败，再验证渠道/订阅映射启停、参数保留、IAM 原因和 CAS、权限隐藏、409 不重试，以及原模型目录行为。该修复随下面的价格页面修复一起发布，没有部署后端。

如需回退线上映射，通过同一接口提交 `enabled=false`、最新详情版本及回退原因，不回滚模型目录开关或价格。

## 用户价格页缺失与保存失败

用户继续报告 `/pricing` 缺少模型。两次独立 GET `/api/pricing` 均只返回原有 5 个模型，断言 `deepseek-flash` 存在失败。线上目录参考价为输入 0.30、输出 1.20、缓存读取 0.06 USD / 1M tokens，但 `ModelPrice`、`ModelRatio` 和 `CompletionRatio` 均没有该模型。公共价格接口只展示有实际计费配置的可用公开模型，不会把目录参考价自动作为收费配置。

用户管理员页面还显示 PUT `/api/option/` 返回 400。共享 `prepareManagedWrite` 把 `expected_revision` 和自动生成的 `expected_revisions` 值转成了数字，而该接口的 Go JSON 适配器声明的是字符串，因此请求在权限和 CAS 执行前被解码拒绝。定价页又并行写三份配置，每次请求分别询问原因，造成同一次保存连续出现三个输入框。回归分别复现了数字/字符串不匹配和期望 1 次、实际 3 次的提示次数。

用户追问中文输入后，浏览器兼容回归进一步复现 `new Headers(...)` 对“新模型上架测试”抛出 ByteString 转换错误。原因是前端把 Unicode 原因写入 `x-authorization-reason`，浏览器在发请求前拒绝该请求头；只用英文验证不足以覆盖这一问题。

修复只涉及既有前端链路：系统选项使用字符串版本号；定价保存只询问一次原因，留空或取消不发写请求；保存后刷新 `readonly-pricing`；共享写入器移除浏览器受限的原因请求头，以既有 JSON body 和查询参数传递同一条完整中文原因。后端 `adminRequestReason` 已支持这两种载体，权限、CAS、原因校验和审计继续生效，不需要新增协议或部署后端。

### 价格发布与现场验收

北京时间 16:17:25，通过 actor 1 现有 IAM 会话和正常 PUT `/api/option/`，使用 ModelPrice revision 1，仅追加 `deepseek-flash` 的每 token 价格 `input_price=0.0000003`、`output_price=0.0000012`、`cache_read_price=0.00000006`。接口成功，revision 1→2，原有 13 个模型价格逐项比较未变；`billing.pricing.update` 对资源 519 的审计保留完整原因。公共 API 随即返回 6 个模型，真实 `/pricing` 页面显示 `$0.30 / $1.20 / $0.06 / 1M tokens`；计费服务的 `system_options` 视图也能读到同一价格。

用户明确授权“构建并发布前端”后，本地 `npm run build` 完成 TypeScript、Vite 与生产体积预算检查。静态文件发布到 `/opt/web/dist`，先复制新资源、保留旧 hashed assets，最后原子替换 index，不重启容器。最终 index SHA256 为 `9aef82b71bdba50726ec7c636cf08cfd6683ce8939c6e3c9b26e17115b7043df`，公共 HTTP 与本地一致；7 个初始资源、2 个价格页面 chunk 和模型管理 chunk 共 10 个资源逐项核验一致。发布前副本保留于 `/opt/web/dist.bak.deepseek-20261015-1624`；中文补修前副本为 `/opt/web/dist.bak.deepseek-20261015-1631`。

真实页面硬刷新后验证：空原因得到“变更原因不能为空”；输入一次英文原因可以完成保存；补修后输入一次“验证中文原因保存，保持现有售价”，页面显示“价格已保存”。英文保存沿用当前售价，并补全该模型的兼容倍率 `ModelRatio=0.003`、`CompletionRatio=4`，其他已有值与事前快照一致。北京时间 16:31:48 的三条 system.option.pricing.update 审计均为 success，完整保留上述中文原因；只读 UTF-8 查询再次确认。最终 ModelPrice/ModelRatio/CompletionRatio 版本分别为 5/4/4，所有事前已有价格逐项比较未变。

测试先红后绿，最终 11 个相关 Vitest 文件共 86 项通过，覆盖字符串 CAS、中文请求可构造性、中文/英文/空值/取消原因、一次提示、公开价格刷新、映射操作及既有授权和渠道冲突行为。定向 ESLint、TypeScript、生产构建和体积预算均通过。没有新增依赖、数据库迁移或付费推理。

## 其他页面中文原因复核

用户要求继续检查其他页面后，对 `web/src` 中所有原因输入、请求头设置和 HTTP 客户端入口进行了检查。业务管理写入均使用 `adminApiClient` 的共享准备器；IAM 预检/提交、撤销会话和会话角色激活分别通过 `writeIAM` / `activateRoles` 使用 JSON 或查询参数。没有发现页面自行设置 Unicode 原因请求头的旁路。后端管理 guard 从 body/query 读取原因，跨服务 gRPC 对非 ASCII 使用 `x-authorization-reason-bin`；服务内部 HTTP 代理的原因头由 Go 构造，不经过浏览器请求头接口。

新增 [跨入口中文传输回归](../../web/src/lib/admin-write.chinese.test.ts)，用真实 Axios 拦截器和序列化后的 HTTP Request 验证 30 类请求：

- 渠道编辑/启用/删除、测试和余额刷新 GET 动作；模型状态/删除、别名删除、渠道/订阅映射和模型导入。
- 用户状态、订阅账户编辑与单个/批量额度重置、撤销订阅、订阅分组编辑、套餐删除。
- 路由分组/访问授权/结算策略、系统选项、上游成本删除、兑换码删除、日志清理和 OAuth 绑定。
- IAM POST/PATCH/DELETE 与用户会话角色激活。

测试原因包含中文标点和 emoji，确认 body/query 完整保留，GET/DELETE 没有请求体，浏览器请求不带原因头；同时检查 CAS 字段没有被修改。渠道创建/编辑、渠道 GET 动作冲突、IAM 同版本预检提交及会话角色冲突的既有页面测试也改用中文原因，确认业务链路和冲突处理保留中文。

本轮聚焦前端 6 个文件 65 项全部通过，IAM 页面和订阅管理的相关既有测试也通过；后端 `go test ./platform/authz -run '^TestOwnerReasonTransportRejectsAmbiguity$' -count=1` 通过。线上 index 和共享入口脚本与已发布本地制品逐字节相同，因此前述全局修复已覆盖其他页面。本轮只新增/加强回归和记录，没有额外运行逻辑变更，无需重复部署。没有为验收而执行生产删除、退款、权限变更或付费调用。
