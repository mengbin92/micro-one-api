# Micro-One-API v0.30.1 发布：注册奖励接入系统选项并修复金额配置双重换算

> 2026-09-17 · 上一版：[v0.30.0](./release-v0.30.0.md)（2026-09-15）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.30.1)

v0.30.1 是 v0.30.0 之后的 **PATCH 修复版本**，包含 5 个提交。修复管理台"系统选项"中
新用户注册相关金额配置（新用户默认金额 / 邀请人奖励金额 / 被邀请人奖励金额）**配了不
生效且输入被错误换算**的问题：后台保存的 `AmountFor*` 选项此前是只写不读的"死配置"，
注册流程从未读取；前端还存在草稿值被二次换算的 Bug，输入 2 会显示并存成 0.0002。

**无 proto 变更、无数据库迁移、无新增必填配置**。新用户奖励走既有 billing
`TopUpQuota` 账本链路发放；未配置选项时行为与 v0.30.0 完全一致（邀请奖励仍由
`INVITER/INVITEE_BONUS_*` 环境变量兜底）。

## 1. 管理台金额选项输入被二次换算

**根因**：系统选项页特色金额行（`AmountForNewUser/Inviter/Invitee`）在每次渲染时对草稿
值再执行一次 quota→货币换算。草稿里存的是用户输入的显示值，输入 2 立即重新渲染为
0.0002，保存时又把缩小后的值写回，配置 \$2 实际存入 \$0.0002。

**修复**：草稿保持显示单位，仅对未编辑过的服务端返回值做一次换算；保存时统一执行
显示值 → quota 的单次换算。

**影响服务**：管理台前端（web/dist，独立部署，无需重启容器）。**注意**：被该 Bug 写坏的
存量配置（如 2、20）不会自动纠正，需在选项页重新填写保存一次。

## 2. 注册奖励读取 AmountFor* 系统选项

**根因**：`AmountForNewUser/AmountForInviter/AmountForInvitee` 三个选项全仓库只有
admin 服务写入，没有任何读取方；注册时用户余额恒为 0，邀请奖励只认
`INVITER/INVITEE_BONUS_*` 环境变量。

**修复**：identity 注册成功后从 `system_options` 解析三项奖励（兼容 legacy
`QuotaFor*` 别名），经 billing `TopUpQuota` 发放并落账本：

- 新用户默认金额覆盖密码注册与 OAuth 首次注册（`OAuthLogin` 新增 created 标记）；
- 邀请奖励优先选项配置，未配置时回退环境变量；选项显式为 0 表示禁用且不再回落；
- 选项读取失败降级为 0，注册主流程不被配置存储阻塞。

**影响服务**：`identity-service`。

## 3. 注册奖励在 schema 隔离部署下读到空表

**根因**：生产启用 schema 隔离（`IDENTITY_SCHEMA=oneapi_identity`），`system_options`
表只存在于 admin 所属 schema，identity 查询落到不存在的表，按设计静默降级为 0，注册
成功但奖励为 0 且无任何日志信号。

**修复**：按候选表名解析——`ADMIN_SCHEMA` 限定时直接用该 schema，否则先查本库（共享库
部署）再回退到生产约定的 `oneapi_admin.system_options`；所有候选不可读时记录告警日志
而非静默失败。Compose 模板同步向 identity-service 透传 `ADMIN_SCHEMA`。

**影响服务**：`identity-service`；`deployments/docker-compose/docker-compose.yml`。

## 4. CI 修复：integration mock 补齐 GetSystemOption

**根因**：`IdentityRepo` 接口新增 `GetSystemOption` 后，`internal/integration` 的
`testIdentityRepo` 未同步实现，包编译失败，govulncheck 安全扫描与 Integration 两个 CI
任务同时失败。

**修复**：补齐 mock 方法（返回空，集成测试不涉及奖励选项）。

## 兼容性说明

- 无 proto 变更、无数据库迁移、无新增必填配置；`ADMIN_SCHEMA` 未设置时按候选链回退，
  行为不变。
- 邀请奖励环境变量 `INVITER_BONUS_AMOUNT/QUOTA`、`INVITEE_BONUS_AMOUNT/QUOTA` 继续有效，
  仅在对应选项未配置时兜底。
- 被前端 Bug 写坏的存量金额配置需在选项页重新保存一次。

## 升级步骤

1. 拉取 `v0.30.1` 镜像（或按部署脚本重建）：`identity-service` 必须更新，其余服务无变化。
2. 前端：重新构建 `web` 并替换宿主机 `/opt/web/dist`（无需重启容器）。
3. 如需让 identity 直连 admin 选项表（schema 隔离部署），在 compose 环境为
   identity-service 设置 `ADMIN_SCHEMA`（模板已透传）；未设置时自动回退到
   `oneapi_admin.system_options` 约定。

## 验证

- `go test ./app/identity/...`、`go test ./internal/integration/` 全部通过；
  `govulncheck` 0 漏洞。
- 生产验证：配置 `AmountForNewUser=50000`（\$5）后新注册用户到账 \$5，账本（ledger）
  含 `system_register` 记录；identity 日志无 `system_options` 读取告警。

## 完整变更日志

- `a9e5e990` fix(test): implement GetSystemOption on integration identity repo mock
- `ad513751` fix(identity): resolve system_options across schemas for registration rewards
- `0fa5b75b` feat(identity): credit registration rewards from AmountFor* system options
- `68a8d21b` fix(web): stop double-converting amount options in system options page
- `f9d7864f` fix(test): serve routing mock through http.Server with read timeouts
