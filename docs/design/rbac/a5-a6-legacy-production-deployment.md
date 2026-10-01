# A4–A6 生产更新（保持 legacy）

> 2026-10-01 · 实现提交 `26a531822bab03f72bdae9a46aab0523801c1f7c` · 最终复查 14:38:22 Asia/Shanghai。

用户在 A5/A6 验收后授权先更新线上环境，再合并到 develop。本次从实现提交的纯 Git 快照在 Apple Silicon 本机构建 linux/amd64 镜像，两个镜像均完成后才更新线上；服务器没有构建镜像，授权事实源继续 legacy。

## 范围与结果

线上此前全部服务为 A3 提交 `36759548`。本次更新 identity-service 和 admin-api，覆盖尚未上线的 A4 会话/账号原子写与 A5/A6 治理/API；其他七个服务的本次运行时代码无变化，其实际镜像保留原版本。前端改动只有生成的 TypeScript 类型，保留主机 `/opt/web/dist`，实际返回的 index SHA256 与发布前一致。

| 服务 | 实际 OCI 镜像摘要 | 验证 |
|---|---|---|
| identity-service | `sha256:8189b4bc7b5ee1f2e29dee939418f6c44221c4ecbfdd1012dbaf3aefa673aa35` | revision `26a53182`，linux/amd64，healthz 200，重启 0，启动错误 0 |
| admin-api | `sha256:9452f119c718e6651da3ccd594ef6ad5212176a8e82a5de3ae67ef37b4b643be` | revision `26a53182`，linux/amd64，healthz 200，重启 0，启动错误 0 |

镜像归档 SHA256、归档内 OCI 索引 blob 摘要及服务器实际 image ID 严格对应；没有引入备用摘要或跳过校验。运行中的原镜像先保存为统一回滚标签，使用原 Compose 执行 `up -d --no-deps --no-build --pull never`，更新与最终独立复查均健康。服务器 `.env`、Compose 和两个服务环境变量摘要与发布前一致。

## 生产验收

- 后台和 identity healthz、relay healthz、公开 `/api/status` 返回 200。
- 既有 ADMIN_TOKEN 的后台 access、用户列表（identity RPC）和渠道列表（channel RPC）只读链路返回 200，响应 success 没有失败。
- 无凭证的旧管理/用户 self、新 IAM 角色列表、本人 authorization 与 relay models 返回 401；identity 直接本人 authorization 同样拒绝匿名请求。
- 静态 ADMIN_TOKEN 调用普通 IAM 角色列表返回 401，不能进入用户会话治理链路。
- 没有发起付费模型请求、通知、注册、账号变更、IAM 管理写或凭证救援来制造验收流量。

生产 SQL 在前后两次均为 `authorization_mode=legacy`、`cutover_state=idle`、policy/catalog revision 1；IAM 分配、委派、会话均为 0，非 draft 或持久化 bound 目录为 0。本次没有新 DDL，现存 110/111 IAM 表通过只读查询确认；没有模式切换、目录启用、候选回填或 D0/D1 交接操作。A5/A6 管理功能仍只在隔离 iam/complete 环境开放验收。

## 回滚与证据

原运行镜像统一保存在 `rollback-20261001-143622`，发布暂存目录为 `/opt/micro-one-api/deploy-rbac-a5-a6-26a53182`。目录权限 0700，保留 rollback.json、构建/发布清单、原 Compose/环境备份和权限 0600 的 identity single-transaction 数据库快照；上传镜像压缩包已清理。本次没有触发回滚。

回滚时按 rollback.json 中原运行镜像恢复两个服务 latest 标签，再以原 Compose 逐服务执行 `up -d --no-deps --no-build --pull never`，核对健康和重启数。账号可能持续写入，不应把数据库快照覆盖恢复作为常规镜像回滚。

完整无凭证证据：[发布证据](../../runbooks/evidence/rbac-a5-a6-legacy-deploy-2026-10-01.json)。本机实现验收见 [A5](./a5-governance-delivery.md)、[A6](./a6-management-api-delivery.md)：实际三库 IAM race、真实 HTTP/gRPC、make all/Wire/迁移/入口契约/make verify 均通过。服务验证成功后合并实现与发布证据到 develop；本次不是版本 release，不创建 main/tag 或变更生产 IAM 模式。
