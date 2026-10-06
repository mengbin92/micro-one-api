# A3 develop 生产更新（保持 legacy）

> 2026-10-01 · 合并代码 `36759548` · A3 提交 `39883ca7` · 最终复查 2026-10-01 06:37:07 Asia/Shanghai。

用户明确授权更新全部生产服务，并确认继续使用 legacy 授权。A3 提交后以 no-ff 合并到 develop，随后从合并提交的纯 Git 快照在本机交叉构建 linux/amd64 镜像。全部构建完成后才更新生产，没有在服务器构建，也没有执行 IAM 事实源切换。

## 更新范围与结果

生产 9 个项目服务全部替换为同一提交：identity、channel、billing、config、log、monitor、notify、admin 和 relay。各容器实际 image ID 与上传镜像一致，revision label 为 `36759548`；两次健康检查均返回 200，最终重启计数均为 0，最近 100 行日志未发现 error/fatal/panic。后台和网关现有端口继续服务流量。

后台 `/api/status` 和持有既有管理员凭证的 access、用户与渠道读取链路返回 200；无凭证的后台管理、用户 self 和网关 models 请求返回 401。没有调用计费模型请求或发送外部通知来制造验收流量。

前端逐文件核对的 169 个构建产物均已与 develop 一致，保留主机 `/opt/web/dist`；服务实际返回的 index 与磁盘及本次构建 SHA256 相同。服务器专用 Compose、环境变量与挂载配置全部保留。

## 迁移与授权状态

迁移前按 ownership 只读核对所有分库，只有 identity 存在待迁移项。先保存该分库的 single-transaction 数据库快照，再用本机构建的同提交迁移工具应用以下三项：

- `103_create_identity_token_quota_dedupe`
- `110_create_identity_iam`
- `111_add_iam_cardinality_limits`

重复执行返回 `nothing to apply`。迁移后及全部服务更新后的 SQL 复查均确认 `authorization_mode=legacy`、`cutover_state=idle`，policy revision 为 1、两项上限为 NULL，IAM 用户角色分配为 0，没有 enabled/bound 权限。110 的用户授权 revision 初始化为 1，未改动旧 role 授权规则。其他分库没有新增迁移。

A3 约束引擎及存储基础已部署，但当前仍没有生产 `IAMChangeAuthorizer` 或 IAM 管理入口绑定。A4–D1 的实际执行链、会话、治理、影子比较与停写交接门槛继续有效。

## 回滚与证据

全部原容器实际镜像统一保存为 `rollback-20261001-063501`，而非假定 latest 标签就是原运行镜像。生产暂存目录 `/opt/micro-one-api/deploy-rbac-a3-36759548` 保留镜像对应表、受保护的配置备份和 identity 数据库快照；已加载的上传压缩包已清理。发布脚本提供更新失败时恢复旧镜像的处理，本次更新没有触发回滚。

需要回滚时，以 `rollback.json` 的原镜像恢复对应 latest 标签，再用原 Compose 执行 `up -d --no-deps --no-build --pull never` 并逐服务检查健康。保持 additive 的 legacy 数据库结构；不要在运行中的账号写入期间覆盖恢复数据库快照。

完整无凭证证据见 [发布证据](../../runbooks/evidence/rbac-a3-legacy-deploy-2026-10-01.json)。本机验收沿用 A3 的实际三库、定向 race、生成/架构/迁移/P0 与 `make verify` 结果，合并后再次检查 P0（583 行）与 diff。
