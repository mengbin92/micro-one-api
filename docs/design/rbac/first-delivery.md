# RBAC 开始实现前的第一批交付

> 2026-09-30 · 复核基线 `951f1686` · `codex/rbac-first-delivery`。
> 完成实施方案第 9 节四项交付；生产继续 legacy，无部署、事实源切换或自定义角色开放。

## 审查入口

| 交付 | 可审查文件 |
|---|---|
| 每个 HTTP 注册、完整 RPC、实际 gRPC 构造及相关 helper 源码 | [entry-matrix.csv](./entry-matrix.csv)：254 HTTP + 178 RPC + 10 gRPC 注册 + 134 SOURCE 摘要；首六列来自源码 AST，其余列明确分类和后续测试义务 |
| 所有账号写者、财务写通道、旧等级/共享凭证矩阵 | [account-writers.md](./account-writers.md) |
| Context/Actor/Scope/GrantSource/ObjectFacts/Decision/Versions 与固定 registry | `domain/authorization/{model,scope,registry,decision,state}.go`，无客户端/仓储/缓存 |
| 公共 DTO 与错误 reason | `api/common/v1/authorization.proto`、`api/{identity,admin}/v1/error_reason.proto`；identity biz `iam_errors.go` |
| 角色/分配 DO、纯继承来源与未来窗口反例、opaque 事务接口 | `app/identity/internal/biz/iam.go` |
| 锁顺序、快照 isolation、模式写门槛、来源/batch 与迁移所有权输入 | [transaction-contract.md](./transaction-contract.md) |
| 可运行语义/错误/JSON/入口漂移反例 | `domain/authorization/authorization_test.go`、identity biz `iam_test.go`、common API `authorization_test.go`、`cmd/rbac-contract-check/main_test.go` |

矩阵的 operations/fields/scopes 是冻结的目标授权契约，legacy_guard 描述当前行为；test_contract 是 A/B 后续测试要求。PUBLIC/SELF/SYSTEM 分类不宣称现有凭证链已符合 IAM；直接 HTTP 用户检查缺口和共享 SERVICE_TOKEN 不能辨认服务身份均明确保留为 B0/B 义务。SOURCE 摘要是用于复核 helper 分支的保守检查，不是额外 endpoint。

HTTP 前缀行列出内部 dispatch，实际不支持的 method 仍是 405；动态 `/api/oauth/` + name 对应 oidc/lark/wechat，provider 可选注册条件保留在源码摘要。HTTP 别名和代理不自动注册新能力，proto annotations 的生成 HTTP handler 未被实际 server 注册时不计作运行入口。relay alternate enhanced 构造与 admin 的两处 gRPC 构造都列入清单；部署是否实际启用在 D 阶段核验。

## 固定语义

Scope 为有限 clauses 并集，每个 clause 内 all/self/user/resource/group 条件取交；all 必须单独成子句，空子句/未知条件/负 ID/额外 JSON 拒绝，空 scope 不匹配任何对象。role_scope 和 assignment_boundary 逐来源求交，保留资源/owner 条件；禁止先各自并集再拼接。读命中任一组，整体写要求 eligible 路径共同覆盖全部受影响组；deny 命中任一组即拒绝，且不被分配允许边界缩小或角色激活规避。资源创建无真实 ID，不能用 resource_ids 自动放行；空归属仅能由适用 ID/all 来源覆盖。

Operation 范围/context/读写覆盖规则由固定 registry 取得；测试只模拟经过代码绑定的 execution point，生产 registry 全部 unbound。组织 context 规范形状可表示，但 RequirePlatform/Decide/IAMSources 拒绝组织；成员/部门目录资格仅 organization，不能借 platform 调用。protected 目录核心禁止 disable/archive/unbind，root role 不能普通变更；完整目标用户保护/救援用例属于 A4/A5/B1。

继承方向 senior→junior，每条 assignment 路径保留来源与边界，active junior 不激活 senior 的能力；disabled 节点切断该路径，其他路径独立有效。时间为服务端 UTC `[starts_at, expires_at)`，nil expiry 为无限期；未来 starts_at 也使快照失效。未来名额示例按用户去重，拒绝重叠，允许相邻窗口；完整 SSD/DSD、启用/图变更影响和 session 未来约束在 A3，首批不冒充完整治理器。

主设计五项目录补齐全部入 registry，均 unbound；源码另外有独立真实能力需精确 code：

| 补齐 | 真实 handler/RPC |
|---|---|
| channel.model_alias.read/create/delete | ListModelAliases/CreateModelAlias/DeleteModelAlias；模型详情 /aliases 分支 |
| channel.model_usage.read | ListModelUsageStats；模型/渠道 /usage-stats 分支 |
| channel.usage_semantic_block.list/resolve | ListUsageSemanticBlocks/ResolveUsageSemanticBlock；RecordUsageSemanticVerdict 独立 relay 系统 capability |
| log.selection_event.list | /v1/selection-events、HandleListSelectionAudit；独立于用户请求内容日志 |

`billing.report.export` 不代替普通读取；当前 `/api/log/export` 为账本 CSV，将要求 ledger.read + 财务导出，未来 cost:export 仍未实现。`/api/status` POST 是 Alipay 回调别名，继续验证 provider signature/order/amount。`/api/group` 管理 legacy group-ratio options，与 routing-group 实体不是同一写。

## 复验

```sh
make all
make rbac-contract-check
go test ./domain/authorization ./app/identity/internal/biz ./api/common/v1 ./cmd/rbac-contract-check
go test -race ./domain/authorization ./app/identity/internal/biz ./cmd/rbac-contract-check
make wire-check
make verify
```

本批上述检查通过。make verify 覆盖格式、全仓 unit/既定 race、架构、迁移静态治理、生成前端类型与前端 lint/test/build；新增授权 package/identity 另跑定向 race。PB/OpenAPI/Wire 由 make 生成，遵循当前 `.gitignore`；未手改生成物，未新增依赖。

入口检查的负例验证新增未分类路由、缺失 caller、未知 code、helper 摘要漂移会失败；这只验证清单覆盖，不证明后端已经执行权限。修改执行点时先 `go run ./cmd/rbac-contract-check -dump` 查看新事实，再人工更新矩阵后六列并核对相关 helper；禁止脚本按 HTTP verb/RPC 名自动推导或授予权限。

未执行的后续验收：三库 fresh/repeat/negative/CAS/锁/回滚，用户/服务双验证及 SQL 范围一致性，全部 IAM 资源管理/约束/委派，前端权限页面与专项 Playwright，候选/影子/停写/生产切换。下一包为 A2，无生产部署动作。
