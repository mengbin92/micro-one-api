# Micro-One-API v0.22.1 发布：OAuth adaptor SSRF 安全修复

> 2026-08-21 · 上一版：[v0.22.0](./release-v0.22.0.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.22.1)

v0.22.1 是 v0.22.0 的 **PATCH 安全修复版本**，修复 OAuth adaptor 在执行
channel-controlled `BaseURL` 请求前未复用上游 URL SSRF 校验的问题，并为 adaptor
JSON 响应补充浏览器内容嗅探防护。

**无 API / proto 破坏性变更、无数据库 schema 迁移、无新增配置项**。本版本只需
重新部署 `relay-gateway`；其余服务无需变更。

## 1. OAuth adaptor SSRF 防护

**根因**：OAuth adaptor 构造完成的上游请求直接调用 `client.Do`，没有经过 provider
路径已有的私有地址、保留地址和非法目标校验。具备渠道配置权限的操作者可能将
`BaseURL` 指向内部网络地址，relay 运行时则会代表请求方访问该地址。

**修复**：在任何 outbound call 前校验最终请求 URL；解析到 loopback、私有或保留
地址时返回 502，不发起上游请求。新增回归测试覆盖私有地址拒绝和正常请求路径。

**影响服务**：`relay-gateway`。

## 2. Adaptor JSON 响应内容嗅探防护

**根因**：adaptor 成功响应虽然声明为 JSON，但没有显式禁止浏览器内容嗅探。

**修复**：设置 `X-Content-Type-Options: nosniff`，并补充响应头回归断言。

**影响服务**：`relay-gateway`。

## 兼容性说明

- **API / proto**：无破坏性变更；正常响应仅新增安全响应头。
- **数据库**：无 schema migration。
- **配置**：无新增配置项。生产环境保持 `PROVIDER_DISABLE_SSRF_CHECK` 未设置或关闭。
- **运行时**：指向私有或保留地址的 OAuth adaptor 请求现在会被拒绝并返回 502；
  合法公网目标行为不变。

## 升级步骤

```bash
git fetch --tags
git checkout v0.22.1

# 仅重新构建并部署 relay-gateway，无需执行数据库迁移
./scripts/deploy-update.sh relay-gateway
```

## 验证

- `go test ./domain/upstream/provider ./internal/server`：通过。
- 发布前必须执行 `make verify` 和 release E2E / Playwright gate。
- 部署后确认 relay-gateway 无重启异常，私有上游地址返回 502，合法 OAuth adaptor
  请求带有 `X-Content-Type-Options: nosniff`。

## 完整变更日志

- fix(security): close OAuth adaptor code scanning findings
