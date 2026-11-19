# Micro-One API v0.46.1 发布公告

> 2026-11-19 · 上一版 [v0.46.0](./release-v0.46.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.46.1)

v0.46.1 是 **PATCH 修复版本**，恢复固定路由组 Key 创建时的会话安全验证流程。

## 1. 分组接口保留安全验证原因

**根因**。Identity 拒绝未经近期安全验证的 Key 创建请求。Admin 将该响应转换为普通 403，省略了管理台打开验证框所需的错误原因。

**修复**。分组接口使用标准响应处理 `STEP_UP_REQUIRED` 与 `MFA_ENROLLMENT_REQUIRED`。管理台可以识别验证要求。用户完成近期安全验证后，页面通过现有流程重试请求。

**影响服务**。Admin API。

## 兼容性说明

通用权限拒绝沿用现有响应格式。Identity 继续负责会话验证和 Key 创建判定。无需数据库迁移或更新 Web 静态产物。

## 升级步骤

1. 构建 linux/amd64 Admin API 镜像并保留线上镜像的回滚标签。
2. 部署 Admin API，核对镜像来源和运行状态。
3. 使用具有分组授权且未完成近期安全验证的用户会话发起 Key 创建，确认页面提示验证并在验证后重试。

回滚时恢复 Admin API 的上一版镜像。

## 验证

- [x] 定向回归先红后绿，验证两个安全验证原因与普通分组 403。
- [x] 综合发布门禁 `make verify` 通过。高强度机器评审覆盖两个运行时代码文件并正常结束，无发现。
- [ ] 生产 Admin 部署和用户安全验证。

## 完整变更日志

- `fix(admin): preserve routing token security verification reasons`
- `docs(release): v0.46.1`
