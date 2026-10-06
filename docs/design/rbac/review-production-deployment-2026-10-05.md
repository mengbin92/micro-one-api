# RBAC 设计复审修复生产更新

2026-10-05 · 用户明确授权更新线上并合并提交到 develop · 修复源码提交 `7969a3cb`。

[设计复审的 14 项修复](design-review-2026-10-05.md) 已部署到生产。九个服务统一更新为 `rbac-review-42b21ad4`，源码摘要为 `42b21ad47e4b29d0b411307ba5faf5c04aaf5a5e933cd7cbab6cdc9f475bf741`。前端单独发布，新增 123 迁移仅作用于 identity。IAM 仍为 **iam/complete，policy revision 7、catalog revision 2**，没有再次切换事实源或改写权限分配。

[脱敏部署证据](../../runbooks/evidence/rbac-review-deploy-2026-10-05.json) 记录实际镜像、回滚标签、迁移、前端、15 项 HTTP/业务链路检查和 5 项 gRPC 边界检查。数据库备份、原始响应、容器环境与 Compose 快照保留在远端 0700 私密部署目录；凭证和用户令牌不进入 Git。

## 执行与回滚材料

所有镜像均在本机按标准 Dockerfile 交叉构建 linux/amd64，再打包、校验摘要并运输；服务器没有构建。更新前九服务健康，本人首页/账本与实际数据库的基线检查通过；identity 全库备份完成。

| 时间（Asia/Shanghai） | 实际结果 |
|---|---|
| 18:21:47 | `123_add_iam_permission_metadata` 应用并复验；重复执行返回 `nothing to apply` |
| 18:30:03 | 九个服务逐一替换完成，逐服务 HTTP 健康和 restart 0 验证通过 |
| 18:30:40 | 前端 173 个发布文件及实际 HTTP 首页摘要核验通过 |
| 18:37:23 | 最终管理、本人用量、gRPC、版本及权限事实检查通过；随后服务身份/源码预检为 ready，零 blocker |

迁移使用只对 `iam_permissions` DDL 和 `schema_migrations` 元数据具有必要权限的临时维护账号，成功后删除。没有恢复已锁定的共享远程 root，也没有向普通服务安装 DDL 凭证。237 条权限记录原有的 ID、资源、code、名称、分类、状态、风险和 revision 联合摘要在迁移与最终验收时均保持不变；新增列均取默认值。

旧 IAM 兼容镜像保留统一 `rollback-rbac-review-20261005-102938` 标签，其中 billing 包含当天已修复的本人用量调用能力。Compose/.env 和 identity 数据有更新前备份；前端保留旧资源备份，先发布新静态资源再原子替换 index，旧 hash 资源保留供已打开页面使用。运输用临时压缩包已删除。

## 线上验收

- 新权限目录支持 `sort,id` 排序。零值元数据按 protojson 正常省略；通过服务端**只读模拟**验证非空 description 和非零 sort 经 admin → identity 完整返回，没有提交元数据修改。
- root 本人授权、角色、用户、渠道、模型、配置和路由组读取正常；普通用户即使 JWT 数值 role=100 也没有 console 权限。匿名和静态 ADMIN_TOKEN 的管理请求拒绝。
- 5 项真实 gRPC：专属 admin/root 成功；普通用户 numeric-root、共享 token/root、缺少 operator、错误 caller/root 分别拒绝。另有既有资源执行点的实际读取检查。
- 本人首页累计值、近 7 天用量、本人账本和伪造 URL user_id 场景通过原始回归脚本与数据库核对，保留之前的专属 identity → billing 修复。
- 九服务最终运行、restart 0，容器环境逐项与更新前一致。固定 caller map、九个独立 outbound 身份、owner SQL 通道一致性和实际源码摘要预检通过。
- 救援继续关闭，旧远程 root 继续锁定，临时 DDL 账号不存在；IAM 模式、切换状态、策略/目录版本和权限事实未变。

验收发现 Compose 的旧源码标签会覆盖新镜像内的源码标签，已只更新来源摘要并逐服务重建复验；服务凭证、DSN、运行时业务配置和数据库用户权限未改动。gRPC 旧验收工具需要显式输出文件参数，按其完整调用约定执行后五项全部通过。这些是部署元数据和验收脚本调整，不涉及追加业务代码修改。

前端生产 build/体积预算通过；设计复审阶段的单元、三库 IAM race、三库迁移、真实 HTTP/gRPC 集成、213 个前端测试和 7 组 Chrome 场景证据见复审记录。本轮发布已验证的同一份源码，未新增版本标签或 GitHub Release。
