# Micro-One-API v0.36.1 发布：普通 WS 长连接修复、Lite 首请求恢复与计时验收

> 2026-10-18 · 上一版：[v0.36.0](./release-v0.36.0.md)（2026-10-17）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.1)

v0.36.1 是 v0.36.0 之后的 **PATCH 修复版本**：修复普通 Responses WebSocket 在第 33 轮被误切断、Lite 空环境因缺独立服务身份导致首请求 503，以及计时摘要导出告警漏报；补齐原生 Linux/amd64 长时性能、多轮 WS 与 Lite 空卷恢复的隔离证据，并提供双语 README / Lite Quickstart。无新增迁移、无 proto/API 破坏性变更。运行时修复影响 `relay-gateway`；Lite 身份配置需由各接收方与调用方统一安装，监控规则按既有流程合并。

本次发布版本制品，**不更新正在观察的生产实例**。固定 48 小时窗口仍于 **2026-10-20 10:10:20 CST** 截止；生产 WS、存储稳态、供应商账期和 executor 同窗条件不因发版自动通过。

## 修复内容

### 1. 普通 Responses WS 不再误触持久化历史上限

**根因**：转发器始终设置 `durableRecord` 回调，该回调还负责契约反馈；relay 以回调是否存在判断是否启用持久化历史限制，导致没有启用持久化交付的普通连接也在第 33 轮断开。

**修复**：按预留实际返回的 `ExecutionDeliveryVersion` 启用持久化硬限制，普通模式继续使用有界历史淘汰。真实持久化模式保留原有 32 轮保护，没有扩大交付或收费能力。

**影响服务**：`relay-gateway`。新增真实 WebSocket / 本地 gRPC 集成测试，验证逐轮摘要、独立观测与业务 root、预留/提交关联、首轮连接阶段和关闭清理；旧判断通过 overlay 可稳定复现失败，修复后 1200 轮 race 长跑通过。

### 2. Lite 空环境恢复首个模型请求

**根因**：旧指南与 Lite smoke 仅生成共享 `SERVICE_TOKEN`，而 `ClaimUpstreamExecution` 要求独立 Relay 身份。共享令牌不会获得该系统能力，即使 IAM 仍处于 legacy 模式，首请求也会被拒绝并返回 503。

**修复**：复用已有 `prepare-rbac-service-identities.py` 和 `check-service-identities.py`，生成九个独立 outbound token 及按固定 RPC 权限生成的接收方信任表。中英文指南、Lite 示例注释与 smoke 同步更新；启动前增加实际 Compose 身份配置回归，没有放宽授权检查。

**影响服务**：Lite 部署的九个服务身份配置；服务鉴权实现与 IAM 模式不变。已有独立身份的环境保留原密钥并校验，不要求轮换。

### 3. 计时摘要导出失败可触发告警

**根因**：实际 exporter 产生 `full/closed/failed/invalid/turn_capacity`，规则却匹配 `dropped|error`，队列满、关闭、导出失败及非法摘要等情况会漏报。

**修复**：告警匹配真实标签；补充触发、等待期、恢复、成功流量与无流量测试，CI 纳入五组 Prometheus 规则测试。本轮此前已保留生产原有规则并追加四条 request-timing 规则后热加载，未重启业务实例。

**影响服务**：Prometheus 规则与 CI。此次打标签不再次修改生产监控配置。[告警与部署证据](../runbooks/post-v0.36.0-acceptance-2026-10-18.md#1-告警反馈环与部署)

### 4. 隔离验收与双语交付

**根因**：早前 arm64 短时性能实验固定关闭→开启，四次中两次超过 5% 门槛；生产尚无 WS 样本。双语指南虽已通过静态检查，仍缺本轮完整构建与空卷恢复证据。

**修复**：性能测试改为 64 并发、四组成对交替、每模式至少 30 秒，分开稳态与排空成本，解析 OTLP 并核对实际接收数；在独立原生 Linux/amd64、4 CPU / 1 GiB 容器完成测量。Lite smoke 补充 SQLite/Redis/.env 停机备份、第二项目空卷恢复与原 Token/渠道解密检查。精简中文 README，增加英文 README 与完整英文 Lite Quickstart，保持命令一致。

**影响服务**：测试、文档与部署示例；性能采集生产逻辑、价格和账本不变。[完整隔离验收](../runbooks/p1-p2-isolated-acceptance-2026-10-18.md)

## 兼容性说明

- 相对 v0.36.0 无新增数据库迁移或 proto 字段/RPC，不修改历史价格、账本或授权绑定；更早版本升级仍需遵循 v0.36.0 的 137/138 迁移说明。
- 普通 WS 可持续多轮；真实持久化 WS 原有 32 轮保护保留，不能把 1200 轮普通模式测试当作持久化模式承诺。
- Lite 的独立服务身份是现有执行准入要求，共享令牌不是替代品；已有部署保留 JWT、渠道加密密钥及服务身份，不盲目重新生成。
- 前端业务代码未变，主生产 `/opt/web/dist` 无需因本补丁重建或替换。README 与 Quickstart 为文档更新。
- 隔离 P95 通过不代表整体容量准入：摘要 sink 为内存回调，未覆盖 Log RPC/SQL；CPU/分配仍有开销。旧 arm64 短测失败与生产零样本边界均保留。

## 升级步骤

1. 记录当前镜像、配置、服务实例和观察窗口，备份 `.env`、数据库与必要存储。本次发布不对生产执行升级或重启。
2. 当前 48 小时观察结束并形成结论后，再安排 Relay 更新；若提前更换实例，需记录新进程边界，不能把重启前后拼成同一稳态窗口。
3. 从 v0.36.0 升级时无新增迁移；维护窗口内按既有本机交叉构建流程更新 `relay-gateway`：`./scripts/deploy-update.sh relay-gateway`。不在资源受限生产机上构建镜像。
4. Lite 按[中文](../quickstart-lite.md)或[英文指南](../quickstart-lite.en.md)升级。保留已有加密/JWT/服务身份；缺独立身份时，使用既有工具生成并校验后统一配置各接收方与调用方，再重建相关 Lite 容器。不要只更新 Relay 的 outbound token。
5. 告警规则按现有监控流程合并、校验与热加载，保留其他运维规则；已经完成本轮热加载的环境无需重复操作。前端静态资源保持现状。
6. 回滚使用先前兼容镜像及原配置；无新增 schema 可回滚。Lite 恢复须使用同一停机点的 SQLite/Redis 备份和匹配 `.env`，在空卷核验，避免覆盖正在写入的卷。

## 验证

- 原生 Linux/amd64 四组 64 并发长时对照：P95 增量 **1.2294% / 1.1173% / 1.0880% / 1.3211%**，均低于 5%；149,428 条摘要、1,234,804 个 span 完整导出/接收，HTTP 失败、摘要失败/丢弃均为 0。
- 普通 WS 1200 轮、69.81 秒本机 race 长跑：1200 条摘要与 1200 次独立提交，预留关联、独立业务 root、连接阶段、去重与清理通过；持久化历史保护分支保留回归。
- Lite 完整源码构建、迁移、前端、登录、渠道、钱包、Token、首请求与重启通过；SQLite/Redis/.env 在第二项目空卷恢复后，原登录、钱包四项、Redis marker、原 Token 与渠道解密通过。临时资源已清理。
- 完整 `make verify` 通过：后端单测与并发敏感包 race、架构/迁移/RBAC/浏览器结果契约、生成 API 类型，以及前端 568 项测试、191 项关键路径测试、lint 与构建预算均通过。RBAC 876 行清单仅刷新两处已评审 Relay 源摘要，分类与权限不变。
- Relay、requesttrace、gRPC 计时三包专项 race、两项启动身份快速回归、七组双语 Bash 命令一致性/语法检查及 Markdown 本地链接检查通过。
- 生产 48 小时观察与 executor 七天准入继续按原门禁处理；发布流水线沿用 Release E2E → 九服务双架构镜像 → GitHub Release 流程，执行状态可在 [Actions](https://github.com/mengbin92/micro-one-api/actions) 核对。

## 完整变更日志

- `a6691d1d` fix(observability): align timing alerts and record rollout checks
- `6b656cd8` fix(relay): preserve websocket sessions and restore Lite startup
- `docs(release): v0.36.1`
