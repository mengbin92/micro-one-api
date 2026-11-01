# Micro-One-API v0.38.1 发布：用户价格页忙闲时展示修复与部署来源标签校正

> 2026-11-01 · 上一版：[v0.38.0](./release-v0.38.0.md)（2026-11-01）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.38.1)

v0.38.1 是 v0.38.0 之后的 **PATCH 修复版本**：修复线上已配置忙闲时价格（deepseek-flash）但用户侧 `/pricing` 页不展示的问题，并修复共享部署脚本不同步 Compose 来源标签导致的部署元数据偏差。无新增公开 API、数据库迁移、配置项或依赖，不改变 Billing/Relay 计费契约，因此按 PATCH 发布。

**生产升级须同时更新 admin-api 镜像与宿主机 Web dist**（前端为宿主机挂载，重建镜像不更新页面），只替换其中一个不能完成修复。两个工件均已随本次修复部署生产并验证，本 tag 发布后按镜像 tag 正常更新即可。

## 修复内容

### 1. 用户价格接口与页面不展示忙闲时价格（P1）

**根因**：公开价格接口只读取普通模型单价与倍率，启用的忙闲时售价实际存放在 ServiceTierPricing 配置中，从未投影到客户视图；`/pricing` 与 Tokens 模型列表也只渲染普通单价。另发现公共价格请求继承了授权刷新的取消信号，会被授权状态变化取消；档位配置保存后公共价格缓存不失效，页面持续展示旧数据。

**修复**：

- biz 从既有配置 owner 读取档位售价，复用 Billing 校验，按字段白名单构造客户售价 DO；排除采购价格、来源类型与内部来源 ID。
- Admin 公开接口将启用的档位、忙闲时段与可组合的长上下文售价按 USD/1M tokens 投影，经公开模型目录过滤；分时配置可独立提供模型价格，合并按目录大小写规则去重，避免旧普通价格覆盖分时投影；关闭配置保持原路径；读取或校验失败返回错误，不冒充空配置。
- Web 按忙时/闲时与上下文条件列价，相同售价档位合并、不同售价分列，展示时区、周内窗口、节假日、日历有效期与预扣时段规则，区分显式零价与未配置；`/pricing` 与 Tokens 公共价格读取改用 Query 自身取消信号；管理员保存档位配置成功后使公共价格缓存失效。

**影响服务**：`admin-api` 与 Web。无数据库迁移，不修改 Billing/Relay 收费逻辑、生产配置与历史账单。

**回归**：原复现用例 `TestReadonlyPricingIncludesEnabledTimePricing` 修复前缺 `time_pricing`、修复后通过；admin 四层包测试、`go vet`、89 项 Web 聚焦测试、ESLint 与 `tsc`、桌面/手机 × 中英文 4 个 Playwright 场景通过；数据边界覆盖采购字段排除、未公开模型过滤、关闭/错误配置、大小写别名、显式零价与英文无中文残留。见[修复运行记录](../runbooks/pricing-time-display-fix-2026-11-01.md)。

### 2. 共享部署脚本不同步 Compose 来源标签（P2）

**根因**：docker-compose 为服务写死显式 `micro-one-api.source.digest` 标签，而 Compose 标签覆盖镜像标签。共享部署脚本 `deploy-update.sh` 只重写 `image:`，导致部署后容器保留旧来源摘要，镜像内容核对出现"本地与镜像一致、容器不一致"的元数据偏差。

**修复**：脚本重写所选服务块 `image:` 的同时同步 `micro-one-api.source.digest` 标签；同一服务出现重复来源标签时报错拒绝；`docker compose config` 校验生效镜像与来源对；容器启动后断言运行容器的来源标签与本地构建输入一致才报告成功。

**影响服务**：所有经 `deploy-update.sh` 部署的服务；本次已用修复后脚本重部署 `admin-api`，容器镜像摘要（`fc858402…`）、来源标签（`a2576fd0…`）与本地构建输入三者一致。

**回归**：9 个部署脚本测试通过，新增来源标签跟踪、运行容器来源不匹配拒绝、无显式标签时继承镜像摘要场景；原覆盖/回滚/多服务场景保持通过。OCR 机器评审 0 findings。

## 兼容性说明

- **API**：`/api/pricing` 响应新增 `service_tier_prices` 与 `time_pricing` 字段（仅当对应配置启用时），既有字段不变；为只读投影，不影响写路径。
- **数据库**：无新增迁移。
- **配置与依赖**：无新增。生产计价配置保持 v0.38.0 现场状态。
- **资金与历史记录**：不重算历史账单，不改变结算路径。
- **Web**：宿主机挂载 dist 需随 admin-api 一并更新；本次两者均已更新。

## 升级步骤

1. 本补丁的两个工件（admin-api 镜像与 Web dist）已部署生产并验证；新环境按 compose 镜像 tag 更新即可，Web 走宿主机 dist 替换流程。
2. 验证：`/api/pricing` 目标模型包含 `service_tier_prices` 与 `time_pricing`，响应无采购与内部来源字段；桌面与移动端访问 `/pricing` 核对两个时段价格与规则展示。
3. 使用 `deploy-update.sh` 的后续部署自动携带来源标签同步，无需额外动作。

### 回滚限制

- 回滚 admin-api 镜像与 Web dist 需成对进行；保留本次部署的回滚标签 `rollback-20261101-112146` 与 Web dist 备份。
- Compose 来源标签随镜像一并回退，不回写旧标签。

## 验证

| 状态 | 证据与边界 |
| --- | --- |
| ✅ | Go 四层包测试、`go vet`、`make all`、`make verify` 全量门禁通过；RBAC 新 SOURCE 登记已补齐 |
| ✅ | 89 项 Web 聚焦测试、ESLint、`tsc`、桌面/手机 × 中英文 4 个 Playwright 场景通过 |
| ✅ | OCR high（价格修复，9 文件）与 medium（部署补丁，2 文件）机器评审 0 P0/P1 findings |
| ✅ | 生产接口：deepseek-flash 返回 `time_pricing`（Asia/Shanghai、两个忙时窗口、忙时 input 2/output 8）与 `service_tier_prices`（default/flex/priority 三桶），无采购字段泄露 |
| ✅ | 生产部署：admin-api 容器运行健康，镜像摘要、来源标签与本地构建输入三者一致；Web dist 已更新 |
| ✅ | Backend CI 全绿（含历史提交 body 修复，run 41262944157） |

## 完整变更日志（v0.38.0 → v0.38.1）

```
f88b54eb fix(deploy): sync compose source label with deployed image
7852b717 fix(pricing): expose configured time-band prices on customer page
```
