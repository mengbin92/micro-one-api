# Micro-One-API v0.33.6 发布：支付宝回调回执与 Playground 消息重用

> 2026-09-30 · 上一版：[v0.33.5](./release-v0.33.5.md)（2026-09-29）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.6)

v0.33.6 是 v0.33.5 之后的 **PATCH 支付观测与控制台体验版本**：支付宝异步回调成功受理后记录脱敏回执，Playground 支持将历史用户消息填回输入框编辑再发送，并更新前端间接依赖。

**无公共 API/proto、数据库迁移或新增配置项**。运行时更新 `billing-service`，前端需独立发布 `web/dist`；本次生产已完成两者更新。

## 修复与功能

### 1. 支付宝异步回调受理回执

**根因**：订单已支付、资产已发放只能证明账务结果，无法独立确认是否收到了异步 HTTP 回调，因为后台查询也能完成发放。

**修复**：验签、订单字段核对及 `MarkOrderPaid` 成功后记录 `alipay notify accepted`，仅含订单号 SHA-256、订单状态及资产发放状态。无效签名或金额不符不产生成功回执；重复回调仍走原有幂等逻辑。

**影响服务**：`billing-service`。日志不保存完整订单号、买家信息、签名或回调原文。

### 2. Playground 重用历史消息

**根因**：修改并再次尝试历史提示词需要手工复制。

**修复**：历史用户消息增加“重用消息”按钮，填入并聚焦现有输入框；用户编辑并点击发送后，沿用当前设置及会话历史。生成中或 API Key 未验证时禁用按钮。

**影响范围**：管理前端 Playground 页面。

### 3. 依赖与验收记录更新

**根因**：前端间接依赖有补丁更新，阶段清单也缺少生产镜像、回调及静态资源的可核对证据。

**修复**：更新 `ip-address` 至 10.7.2、`brace-expansion` 至 5.0.12、`fast-uri` 至 3.1.8、`undici` 至 7.30.0；归档生产健康和指标、沙箱回调及前端资源哈希，并修正 O5 隔离复测记录。

**影响范围**：前端依赖、阶段计划和运维文档。

## 兼容性说明

- 支付接口、验签条件、账本和幂等发放规则保持兼容，无数据库迁移或配置更新。
- 新增成功受理日志字段，可使用订单哈希关联脱敏验收证据。
- Playground 重用消息需要主动发送，使用当前会话和设置；不是原始请求参数的完整回放。

## 升级步骤

1. 在本机交叉构建 `linux/amd64` 的 `billing-service` 镜像，保存回滚标签并传至生产主机，执行 `docker compose up -d --no-deps billing-service`；不在生产主机构建。
2. 在本机执行 `cd web && npm ci && npm run build`，备份并上传到宿主机 `/opt/web/dist`。前端由目录挂载提供，更新 admin 镜像不能替代静态资源发布。
3. 检查 billing 健康、支付对账指标及公开前端资源哈希。验收支付回调时使用支付宝沙箱，避免归档签名链接或回调敏感字段。

本次生产 billing 镜像为 `sha256:b2120a0a2fdfa2f14e5853e8da1dc6ae4c888c200882d0bccb1055663f1e39c6`，回滚标签为 `docker-compose-billing-service:rollback-20260930-115808`；前端备份为 `/opt/web/dist.bak.20260930-121611`。

## 验证

- billing 全部包测试、支付宝回调 race 测试及架构检查通过；回归覆盖无效签名、金额不符、重复回调只发放一次及日志脱敏。
- Playground、Relay 客户端和凭据相关 12 项测试、修改文件 ESLint、TypeScript/Vite 构建和 6 项产物预算检查通过。
- 发布前部署文档检查通过：Compose 配置、41 个 Kubernetes 资源、83 个引用及 213 个本地 Markdown 文件；提交信息正文检查通过。
- 用户完成 ¥0.01 支付宝沙箱支付后，2026-09-30 12:06:44（北京时间）收到独立 HTTP 成功受理回执；对应订单为 `paid/issued`，充值账本和去重声明各一条。详见 [沙箱回调证据](../runbooks/evidence/f17-sandbox-notify-2026-09-30.json)。
- 生产 Playground HTML 及页面资源 HTTP 200，字节与本地测试构建一致，CSP 保持有效。详见 [前端发布证据](../runbooks/evidence/playground-message-reuse-2026-09-30.json)。
- v0.33.5 相关服务的镜像、回滚镜像、健康检查、gRPC 指标及 9 个 Prometheus 目标均已核对。详见 [生产复核证据](../runbooks/evidence/v0.33.5-production-check-2026-09-30.json)。

实际平台重复投递、支付宝收到 `success` 回应及回调与后台查询的先后顺序仍未独立验证；线上已登录浏览器交互由本地 DOM 回归提供覆盖。O5 延迟归因仍需 DB/宿主观测，不能由现有健康与指标检查推断。

## 完整变更日志

- `172bbff9` fix(deps): bump ip-address 10.4.0 → 10.7.2
- `b9db07cc` docs: record corrected O5 isolation retest
- `66cde24a` chore(deps): bump the npm_and_yarn group across 1 directory with 3 updates (#23)
- `100a7f79` feat(billing): record redacted Alipay notify receipts
- `1c5d1556` feat(web): reuse historical Playground messages
- `9e4a3fd7` docs: record production and sandbox acceptance
- docs(release): v0.33.6
