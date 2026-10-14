# P2 合并前完整验证

2026-10-14，覆盖 P2 工程、DeepSeek 库存/目录修复、渠道动作 revision 冲突和管理端 protobuf 包装请求兼容修复。生产部署结果另行记录；完整代表性账期和生产契约灰度验收仍以两份设计方案的门槛为准。

原 CI [失败任务](https://github.com/mengbin92/micro-one-api/actions/runs/38401111666/job/115308679569) 的日志仅显示两份设计文档使用仓库外个人 Downloads 文件链接。已改为说明性路径文本，完整 `scripts/check-deployment-docs.sh` 通过，包括四套 Compose、Kustomize、41 个 Kubernetes 对象、路由约束和 281 份 Markdown 链接。

| 检查 | 结果 |
|---|---|
| `make all` | proto、config、Wire 生成通过 |
| `make test`、`make test-race`、`make test-integration` | 全部通过；真实外部 e2e 需要独立运行栈，不计入单元命令 |
| MySQL 8.4 / PostgreSQL 16 migration smoke | fresh、repeat、status、失败注入和历史元数据预检通过，包含 132–134 |
| channel / billing / identity data 三库测试 | SQLite、MySQL、PostgreSQL 全部通过；使用本机临时数据库，不使用生产 DSN |
| `make migration-check rbac-contract-check iam-browser-gate-check` | 通过；862 条审查入口和浏览器验收负例通过 |
| 架构边界、`git diff --check` | 通过 |
| `npm test -- --run --maxWorkers=4` | 79 个文件、453 个测试通过 |
| `npm run test:critical -- --maxWorkers=4` | 16 个文件、186 个测试及覆盖率门槛通过 |
| `npm run build` | TypeScript、Vite 与制品预算通过 |

完整并发前端首次运行出现一个默认 5 秒超时；限制 worker 数量后原断言和原超时全部通过。三库测试发现支付并发测试在正式迁移后再次 AutoMigrate，引发 PostgreSQL 约束重命名错误；移除该冗余调用，保留真实迁移和资金竞态断言，聚焦回归与完整三库复测通过。

新增 identity-owned migration 134 仅补齐七项 P2 权限及目录 revision 2，全部为 draft/unbound，没有自动启用、授权或修改既有角色。审查矩阵补齐 19 个入口，更新变更代码的事实摘要和位置；它不生成运行时授权。

关联：[P2 工程交付](billing-upstream-p2-completion-2026-10-14.md)、[DeepSeek 修复](deepseek-catalog-repair-2026-10-14.md)。
