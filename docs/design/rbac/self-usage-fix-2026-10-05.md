# IAM 用户首页与用量统计修复

2026-10-05 · 基于 `develop@d9a0c657`，延续 D1 线上更新及合并 develop 的授权。

用户报告 admin 登录后首页／用量统计多项为 0。线上实际账户已有非零累计消费和请求记录，但 `/api/user/dashboard` 返回 HTTP 200、`success=false`，消息为 `failed to aggregate usage: rpc error: code = PermissionDenied desc = caller capability denied`；`/api/user/logs` 返回 403。前端没有取得统计对象，按既有默认值显示为 0。直连 identity 也复现，排除 admin 代理差异。

## 根因与修复

固定服务调用表遗漏 identity-service 对 billing `ListLedger` 和 `AggregateLedgerByDate` 的调用权限。此前共享服务凭证没有触发专属 caller 拒绝，D1 专属凭证启用后这条用户侧链路被阻断。切换验收检查了管理侧账本和本人授权，未覆盖本人首页统计到真实账务 owner 的完整调用链。

仅为上述两个 RPC 的 `UserCallers` 增加 `identity`。identity 转发已验证的用户会话，billing 再次认证并在查询前应用本人范围；不增加 `SystemCallers`，不开放管理聚合或财务导出。没有修改账务记录、IAM 分配、数据库权限、前端或运行时凭证。

## 回归与线上验证

新增 `TestIAMSelfUsageThroughDedicatedIdentityCaller`：真实 identity HTTP → 专属 identity 凭证 → billing gRPC → IAM 会话解析 → 真实 SQLite 仓储。测试先复现与生产相同的 `caller capability denied`，修复后普通用户及 root 的累计值、近 7 天消费和本人列表均与种子记录一致；URL 伪造 user_id 无效，无用户凭证不能读账本，跨用户查询为空，identity 不能调用管理聚合。

```sh
go test ./internal/integration -run 'TestIAMB3|TestIAMSelfUsageThroughDedicatedIdentityCaller' -count=1
go test -race ./internal/integration -run '^TestIAMSelfUsageThroughDedicatedIdentityCaller$' -count=1
go test ./platform/security/serviceidentity ./platform/grpc/xgrpc ./app/billing/internal/biz ./app/billing/internal/data ./app/identity/internal/server
go run ./cmd/rbac-contract-check
```

以上全部通过，契约检查为 828 行。线上原始复现命令 `ssh <生产主机> python3 /tmp/rbac-user-usage-probe.py` 从失败转为通过：首页 `success=true`、累计值与数据库一致，用量列表 HTTP 200 并返回记录。进一步使用只读验证脚本核对 admin 和普通用户：累计值、近 7 天按日统计、本人账本总数均与数据库一致；普通用户没有近 7 天记录时仍应显示真实的 0。匿名首页及列表为 401；全部九服务健康、restart 0，模式保持 `iam/complete`，旧远程 root 保持锁定。

## 生产交付

billing 在本机交叉构建 linux/amd64 后上传，服务器不构建。11:26:22（Asia/Shanghai）完成单服务替换，实际镜像、来源摘要、回滚标签见 [脱敏证据](../../runbooks/evidence/rbac-self-usage-2026-10-05.json)。Compose 仅替换 billing 镜像，实际容器环境与替换前逐项相同；旧 IAM 兼容镜像留作回滚。统计原始响应及配置备份仅保存在生产私密诊断目录，不进入 Git。

此回归应成为后续 IAM 部署验收必检项：本人首页和账本必须通过真实专属服务链路验证，不能用 management caller 或仅检查健康接口替代。
