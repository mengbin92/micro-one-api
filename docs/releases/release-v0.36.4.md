# Micro-One-API v0.36.4 发布：上游契约类型边界与供应商证据修复

> 2026-10-24 · 上一版：[v0.36.3](./release-v0.36.3.md)（2026-10-21）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.4)

v0.36.4 是 v0.36.3 之后的 **PATCH 修复版本**：上游有限契约在转换前校验字段类型，类型漂移明确失败；修复首次契约灰度对未选中请求的阻断，补齐有到期时间的 DeepSeek 独立契约证明试点。同时收录供应商报告导入、历史费用证据、安全依赖及 CI 修复。新增 Identity 140、142 与 Billing 141 迁移；上游契约证明与用户收费口径分别验证。

**发布准备状态**：本文件为待最终验证的发布制品初稿。此次修复的完整测试、生产部署、契约试点及标签 workflow 结果由下方“验证”补齐；不能将候选资料或历史验收写成本次上线成功。

## 修复与改进

### 1. 参数转换先检查类型，未知类型明确失败

**根因**：有限契约已有不可变内容版本，但 `rename` 原样搬运 JSON，`default` 只区分字段是否存在，二者没有声明被消费字段的预期类型。仅对显式 `reject` 规则做类型检查，不能覆盖整个转换边界。

**修复**：`rename/default` 必须声明已安装类型，编译时检查默认值；执行时在任何变更前核验消费路径与重命名目标。类型不匹配返回包含契约 revision、rule ID、字段路径及预期/实际类型的错误，不记录字段值。保留缺失、null、0 的区别，合法未知普通 JSON 保真；运行时 build 提升为 `finite-contract-runtime-2026-10-24.1`，旧文档须按新类型要求重新审查生成不可变版本。

**影响服务**：`relay-gateway`、`channel-service`；共享契约测试、回放与候选资料同步使用新 build。

### 2. 首次灰度保留既有基线路径

**根因**：首次发布时没有 previous revision，原先未命中 1–5% 灰度的请求会被加入 blocked protocol，导致小范围试点反而拒绝其余请求。

**修复**：首次灰度中未选中的请求沿用既有未绑定 adapter 行为；已有 previous revision 时继续选择明确的旧版本。事故隔离仍阻断对应协议，不能通过“无旧版本”绕过 quarantine。发布的 CAS、独立 fresh proof 及晋级门槛保持。

**影响服务**：`channel-service`、`relay-gateway`。

### 3. DeepSeek 契约证明独立于结算 parser

**根因**：10 月 14 日官方原生样例中的 cache aliases 与 SSE 非终结 usage=null 已有离线候选 parser，但它没有安装到契约证明链路；不能用生产通用 parser 的 unsupported 结果晋级契约，也不能为了试点改变用户收费。

**修复**：为精确 DeepSeek Flash / Chat profile 安装独立 proof parser/usage pair，由冻结的权威契约选择并在 owner 重新解析原生证据；JSON/SSE 证明独立保存。参数契约验证不更换原生结算 parser、算法、收费名单或历史账本。候选资料固定来源 11、精确模型 `deepseek-flash`、`/chat/completions`、Chat inbound、官方依据与独立 fixture SHA-256，只约束 `stream` 与 `stream_options.include_usage` 的 boolean 类型。

**影响服务**：`relay-gateway`、`channel-service` 及承载原生证据的消费者；具体部署顺序以本次验证记录为准。

### 4. 有界探测到期停止，保留库存额度

**根因**：既有预算按来源和 UTC 日计数，缺少 target 到期时间；把每日四次写成“一次试点最多四次”，会在跨日或进程重启后重复执行。DeepSeek 受控文本探测还需要明确关闭 thinking，并为 SSE 请求完整 usage。

**修复**：探测目标增加可选 `expires_at`，Monitor 与 Relay 在准入前检查。此次试点最多四次推理、0.10 CNY 授权上限，到期早于当前 UTC 日切换，结束后撤去付费目标；保留库存原有 300 次额度，并将同来源总预算明确增加四次、512 输入和 64 输出 token。按当前官方高峰单价、每次最多 128 输入/16 输出估计，四次保守上界为 **0.001536 CNY**，该估计不冒充供应商实扣。具体到期值及真实调用结果仍待落盘。

**影响服务**：`monitor-worker`、`relay-gateway`；运维探测配置。

### 5. 供应商报告导入与历史费用证据补齐

**根因**：既有 root 仅有报告读取权限；真实导入按每个 intent 扫描事件且缺少 intent 索引，RPC 超时。既有核验只读取持久执行 attempt，独立核对过的供应商历史 CNY 归档无法保存和回放。

**修复**：Identity 140 仅发布经批准的供应商导入权限；Billing 141 增加 intent 事件索引，按有界批次读取并保留 proof、数量及未知时间限制。追加精确十进制历史费用回放、归档来源与独立终结证据，以不可变后继版本保存更正；前端提供预览、去重提交和结果不确定时回读。既有 203 次归档按原币回放为 2.04700896 CNY、差异为零，正式覆盖仍保持 pending。

**影响服务**：`identity-service`、`billing-service`、独立前端。历史生产与核验记录见[渠道 11 闭窗](../runbooks/deepseek-channel11-closed-window-2026-10-23.md)和[历史费用证据](../runbooks/deepseek-owner-retrospective-2026-10-23.md)。

### 6. 报告写入、安全依赖与 CI 维护

**根因**：内存验收工具的报告路径未限制符号链接边界，前端依赖包含已知安全问题；CI 的迁移期望与已发布迁移不同步，历史资产校验记录也需要保留经审查的例外边界。

**修复**：通过 `os.Root` 限制报告写入及 cgroup 读取范围并覆盖越界链接；更新 `follow-redirects` 1.16.1、Vite 8.1.6；同步迁移期望与经审查的历史资产检查规则。保留扫描与运行时安全边界。

**影响服务**：验收工具、CI、前端依赖与构建制品。

## 兼容性说明

- 从 v0.36.3 升级需确认 Identity 140、142 与 Billing 141；这两项迁移已随 10 月 23 日供应商修复部署的环境无需重复执行。更早环境先完成对应历史迁移。
- `rename/default` 新增显式类型要求，运行时 build 已变化；契约内容 hash、绑定 revision 与事故 revision 继续分别记录，旧契约不能用修改原记录的方式升级。
- `expires_at` 为可选探测配置，只有安装了到期检查的 Monitor/Relay 才能执行本次一次性付费试点；新消费者应先于生产者升级。
- DeepSeek 新 profile 仅用于契约证明。用户价格、钱包、结算 parser、收费 allowlist 与财务覆盖结论不因证明成功而改变。
- 前端继续由宿主机 `/opt/web/dist` 提供，后端镜像升级不能替代前端发布。

## 升级步骤

1. 备份生产 Compose、环境、当前镜像及数据库；核对已有 140/141/142 迁移和权限状态。备份中含秘密的文件只保留在生产、使用最小文件权限。
2. 在本机或 CI 跨构建 linux/amd64 镜像，先升级证据消费者与 Channel，再更新 Relay/Monitor；生产主机只加载镜像。按实际版本差异更新 Billing/Identity 和独立前端。
3. 先确认服务健康与专用服务凭据信任链，再按审核配置启用 Channel/Relay/Monitor 的契约 gate。gate 开启与精确来源绑定发布分别验收。
4. 通过正常 IAM 会话执行明确的权限生命周期与 owner 契约 API，保存不可变候选 revision。按[试点计划](../runbooks/evidence/upstream-contract-types-2026-10-24/pilot-plan.json)配置相同 source 预算、短到期时间与最多四次推理；不得伪造成功证明或扩大收费范围。
5. 独立 canary 通过后才按当前 binding CAS 发布 1–5% 灰度。晋级仍要求至少 30 分钟、100 个完整 verified attempt、零失败与 fresh proof；四次试点不能替代该门槛。完成或失败后移除付费探测目标并记录对账与用户账本无变更检查。
6. 回退使用备份的兼容应用、配置与前端；保留数据库、证据、审计、outbox 和不可变历史。已有绑定来源不能靠关闭 gate 绕过持久隔离。

## 验证

- **待填**：类型漂移 red/green、rename/default/null/零值/嵌套路径与发送前拒绝回归，当前变更的完整单元、竞态、分层及生成检查。
- **待填**：首次灰度基线与 quarantine 回归、独立 DeepSeek JSON/SSE proof/owner 重验/收费隔离、目标到期与共享预算回归。
- **待填**：本次 linux/amd64 制品、部署源码摘要、实际镜像、健康、gate 状态、不可变候选及真实最多四次调用的脱敏证据。不得将计划状态写成已通过。
- **待填**：试点实际 CNY 费用或明确的未知状态、钱包/用户 ledger/用户价格无变更检查、付费目标撤销与到期验证。
- 历史供应商修复的核验、生产制品与原始材料保留于上述 10 月 23 日记录；这些材料不能代替本次契约试点验收。
- **待填**：最终 develop/main CI、Release workflow 与双架构制品结果；只有对应实际 workflow 成功后更新结论。

## 完整变更日志

- `docs: verify v0.36.3 release and remaining field prerequisites`
- `docs: record channel 11 cost verification and bounded live tests`
- `fix(security): confine timing report writes and patch dependencies`
- `fix(ci): allow reviewed historical asset checksum findings`
- `fix(billing): unblock owner supplier report imports`
- `docs(billing): verify DeepSeek costs by request time`
- `fix(ci): align migration test expectations with releases`
- `feat(billing): retain retrospective vendor cost evidence`
- **待填**：本轮类型契约、独立证明、到期探测修复及 `docs(release): v0.36.4` 的最终 Conventional Commit subjects。
