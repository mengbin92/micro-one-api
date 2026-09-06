# 分组整理验证记录

> 2026-09-06 · 分支 `codex/group-concepts-refactor` · 基线 `7fce275`
> 对应 [概念与实现方案](./group-concepts-and-implementation.md)。

## 检查结果

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

## 新增行为覆盖

- 单组、多组、重复成员、成员空白、大小写、空成员列表，以及完整成员匹配。
- HTTP sticky 与 Responses 路由恢复接受合法多组账号，拒绝无成员关系的账号。
- 多组会话命中后不触发普通重选，仍刷新所属请求组的绑定 TTL。
- 内存与 SQLite 的能力同步不会因重复路由成员增加候选权重；无成员关系的组查不到能力。
- 倍率列表默认项及排序、增删兼容、有限正数与单组输入校验、`null` 配置及非法配置处理。
- 读取价格配置失败时不写入存储，避免覆盖已有倍率。
- HTTP 创建额度策略时省略状态默认启用、显式 `status:0` 保持禁用，并核对持久化后读取结果。

## 环境修复与验证边界

首次服务测试发现本地 Proto 生成物落后于当前源文件，按 `make api` / `make test-unit` 的生成步骤修复；
没有手改生成文件。首次前端构建发现本地 `node_modules` 缺少锁文件中的字体包，运行
`npm ci --no-audit --no-fund` 后构建通过；依赖清单与锁文件没有变化。

未运行依赖外部服务的 Compose / Playwright E2E，也未访问生产环境。
本次没有 SQL 或迁移变更；MySQL / PostgreSQL 成员查询一致性、统一模型授权和双写迁移的验收，
属于方案中的后续工作，不能用本次 SQLite 与单元测试结果替代。
