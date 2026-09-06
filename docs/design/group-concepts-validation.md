# 分组整理验证记录

> 2026-09-06 · 分支 `codex/group-concepts-refactor` · 基线 `7fce275`
> 对应 [概念与实现方案](./group-concepts-and-implementation.md)。

## 2026-09-07：清点工具与统一授权

### 后续：按服务拆 schema 的清点支持

环境元数据核验发现用户、渠道、价格配置已按服务拆 schema，单库读取不足以形成完整基线。
审计命令新增 `--identity-schema` / `--options-schema`，在同一 MySQL 只读事务内查询所有表；
schema 标识符严格校验，SQLite 拒绝 schema 覆盖，真实数据报告目录加入 Git 忽略。

`go test -race ./app/channel/cmd/group-audit ./app/channel/internal/data ./app/channel/internal/biz` 通过，
包括原有只读回归、无效 schema 输入与 MySQL 跨 schema SQL 引用。Linux/amd64 命令已在本机交叉编译完成。
真实数据报告导出需明确的数据源与本地目的地授权；准备命令和读取元数据不等于已完成授权基线清点。

### 统一授权阶段验证

上一阶段已提交为 `669d7af8`。本阶段实现和运行方式见
[分组清点与统一路由授权](./group-audit-and-routing-authorization.md)。

| 检查 | 结果 |
|---|---|
| `make all` | 通过，重新生成 API、配置、Wire 并整理依赖 |
| `make test-unit` | 通过，仓库默认后端门禁，包含新审计命令 |
| `make test-race` | 通过，仓库指定并发敏感包 |
| `go test -race ./domain/routing ./app/channel/... ./internal/data` | 通过，补充渠道授权与缓存适配器 |
| `./scripts/check-architecture.sh` | 通过，包含 Wire 注入器编译检查 |
| `make migration-check` | 通过，只有历史 allowlist 提示 |
| `git diff --check` | 通过 |

新增覆盖：

- 内存 / SQLite 下正常选择与指定来源授权一致；保留账号 CSV 之外的模型级授权，并禁止扩展到其他模型。
- 注册表拒绝不能退回 legacy 或 catch-all；模型路由收窄选择、目录和指定来源权限，查询失败不会放行。
- 模型路由变更后清理目录缓存；缓存命中重新授权并更新上游模型 ID，不改写共享缓存对象。
- 粘性账号、Responses 本地缓存及存储路由转发检查当前权限；重试候选及同源重试撤权后不会再次访问上游。
- 审计 CLI 的矩阵和诊断、重复运行输出一致、SQLite 文件内容不变、只读连接拒绝写入、探测不完整不输出报告、报告文件权限及不覆盖旧基线。

生成时发现 billing 的 Wire 源文件缺少 `SetPricingSnapshotRepo`，而已提交生成物具有该接线。
已补齐源文件并重新生成，保留现有计费行为；`go.mod` 及依赖版本未变，`go.sum` 补齐 Wire 工具依赖校验。
最后的定向复核覆盖审计报告排序、Relay 清理和 channel/billing 的 Wire 源文件编译。

本阶段没有前端变更，沿用上一阶段前端检查结果；未执行外部服务 E2E，未连接生产数据库或部署。
MySQL 命令连接流程已实现，但仅 SQLite 完成存储行为验证。实际数据清点、PostgreSQL 验证与成员关系表迁移尚未执行。

## 2026-09-06：兼容概念整理

### 检查结果

| 检查 | 结果 |
|---|---|
| `make test-unit` | 通过，全部默认后端单元测试包 |
| `make test-race` | 通过，订阅、Relay、billing、admin、鉴权等仓库指定包 |
| `go test -race ./domain/routing ./app/channel/internal/data ./app/channel/internal/service` | 通过，包含新增 CSV 重复成员回归 |
| `./scripts/check-architecture.sh` | 通过 |
| `make migration-check` | 通过，仅输出历史 allowlist 中重复编号提示 |
| `cd web && npm run lint` | 通过 |
| `cd web && npm test -- --run` | 通过，47 个测试文件、169 项测试 |
| `cd web && npm run build` | 通过，TypeScript 检查及 Vite 生产构建 |
| `git diff --check` | 通过 |

### 新增行为覆盖

- 单组、多组、重复成员、成员空白、大小写、空成员列表，以及完整成员匹配。
- HTTP sticky 与 Responses 路由恢复接受合法多组账号，拒绝无成员关系的账号。
- 多组会话命中后不触发普通重选，仍刷新所属请求组的绑定 TTL。
- 内存与 SQLite 的能力同步不会因重复路由成员增加候选权重；无成员关系的组查不到能力。
- 倍率列表默认项及排序、增删兼容、有限正数与单组输入校验、`null` 配置及非法配置处理。
- 读取价格配置失败时不写入存储，避免覆盖已有倍率。
- HTTP 创建额度策略时省略状态默认启用、显式 `status:0` 保持禁用，并核对持久化后读取结果。

### 环境修复与验证边界

首次服务测试发现本地 Proto 生成物落后于当前源文件，按 `make api` / `make test-unit` 的生成步骤修复；
没有手改生成文件。首次前端构建发现本地 `node_modules` 缺少锁文件中的字体包，运行
`npm ci --no-audit --no-fund` 后构建通过；依赖清单与锁文件没有变化。

未运行依赖外部服务的 Compose / Playwright E2E，也未访问生产环境。
本次没有 SQL 或迁移变更；MySQL / PostgreSQL 成员查询一致性、统一模型授权和双写迁移的验收，
属于方案中的后续工作，不能用本次 SQLite 与单元测试结果替代。
