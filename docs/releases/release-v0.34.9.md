# Micro-One-API v0.34.9 发布：同步授权刷新冒烟断言，恢复 Release 发布

> 2026-10-07 · 上一版：[v0.34.7](./release-v0.34.7.md)（2026-10-07）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.34.9)

v0.34.9 是 v0.34.7 之后的 **PATCH 发布恢复版本**，完整包含 v0.34.8 候选的用户可用分组、钱包续订／支付创建和权限页面修复，并同步 Release 中独立运行的授权刷新 Playwright 冒烟断言。

**v0.34.8 未完成发布**：Release 的桌面／手机冒烟使用旧行为断言，E2E 门禁失败后 Docker 构建和 GitHub Release 创建均被跳过。保留原 tag 和失败记录，正式版本使用 v0.34.9；不移动旧标签，不跳过门禁。

相对于当前生产已部署的 v0.34.8 修复等价镜像，**本次仅修改测试与文档，不改变运行时、API/proto、数据库或配置**。未部署业务修复的环境需更新 `admin-api`、`channel-service`、`billing-service` 与独立挂载的 `web/dist`；当前生产无需为本次发版重新部署或重启。

## 修复内容

### 1. Release 冒烟匹配授权刷新期间的实际行为

**根因**：`authorization-refresh.spec.ts` 仍断言刷新时「保存目录资料」按钮数量为 0。权限页面已改为保留按钮并禁用执行，桌面与手机项目因此均在旧断言处失败。普通 CI 的单元测试和真实 owner IAM 浏览器矩阵不执行这条通用冒烟用例，不能替代该 Release 门禁。

**修复**：断言刷新前按钮可执行、刷新期间可见且禁用、刷新完成后恢复可执行；继续验证未保存说明、owner 请求计数不变，以及真实权限撤销后弹窗和旧数据被清除。保留完整测试，不增加 skip、不降低拒绝检查。

**影响服务**：无运行时服务变更；影响 Release 的桌面／手机 Playwright 冒烟。

### 2. 正式纳入用户侧分组、购买与支付修复

**根因**：普通用户可用组查询误走后台目录／报价权限；billing 的 `PurchaseSubscription`、`GetRoutingCapabilities` 和独立支付入口使用的 `CreatePaymentOrder` 遗漏在已完成执行点覆盖列表中。

**修复**：内部参考读取使用专用服务能力，同时保留用户事实验证、分组权益过滤及后台管理 operator 检查；补齐三个 billing RPC 覆盖，钱包扣款前由 owner 验证本人账户。完整支付 HTTP→RPC→IAM→套餐快照→SQLite 订单仓储回归验证服务端定价、同键单笔订单、下架后的旧请求重放及拒绝边界。

**影响服务**：`admin-api`、`channel-service`、`billing-service`。这是 v0.34.8 候选已有并已上线的业务修复；细节见 [候选说明](./release-v0.34.8.md) 与 [生产记录](../runbooks/iam-self-service-hotfix-2026-10-07.md)。

### 3. 正式纳入权限页面导航与轮询展示修复

**根因**：每个 IAM 子页面重复渲染导航，刷新期间暂时不可执行的授权状态又控制控件的存在，导致局部内容消失再出现。

**修复**：移除重复子导航，有效授权摘要用于展示，刷新期间禁用动作；真实授权变化继续移除旧页面与受保护数据。本版补齐与该行为一致的 Release 冒烟测试。

**影响服务**：独立挂载的前端 `web/dist`，业务实现已在线上。

## 兼容性说明

- 无 API/proto、数据库迁移或新增配置；本版相对 v0.34.8 候选仅有测试／文档变化。
- 保留 IAM owner 判权、用户本人校验、分组资格过滤与 Idempotency-Key 语义。
- v0.34.8 tag 保留但没有完整 Release 制品，请以 v0.34.9 为正式升级目标。

## 升级步骤

1. 当前生产已部署业务修复，无需因测试断言调整重启服务；保留现有镜像、IAM 状态与回滚备份。
2. 未部署业务修复的环境获取 v0.34.9 源码或该版本发布镜像，在本机交叉构建 `linux/amd64` 并上传，服务器禁止构建。
3. 备份当前镜像和 Compose，明确更新 channel、billing、admin 的固定镜像引用，再按顺序执行 `docker compose up -d --no-deps --no-build --pull never <service>`。
4. 独立构建并同步 `/opt/web/dist`；仅更新 admin 镜像不会更新宿主机挂载前端。
5. 核对实际 image ID、healthz、restart 和前端摘要；带登录会话的支付、回调与权益发放验收使用受控测试套餐。步骤与回滚见 [生产修复记录](../runbooks/iam-self-service-hotfix-2026-10-07.md)。

## 验证

- 定向 Playwright 命令在修复前复现两个平台的 `expected 0 / received 1`，修复后桌面／手机 2 项通过；刷新期间禁用执行、草稿保留、缓存稳定和撤销清理均验证。
- 通用 Playwright 冒烟在 CI 模式、两个 worker 下完整通过：69 项通过；既有 mobile-only 用例在桌面项目跳过 1 项，没有新增 skip 或改弱门禁。定向修复前／后与全量结果见 [v0.34.9 校验证据](../runbooks/evidence/patch-v0.34.9-validation-2026-10-07.json)。
- 测试文件 ESLint 与 `git diff --check` 通过；业务源码没有变化，沿用 [v0.34.8 全仓校验](../runbooks/evidence/patch-v0.34.8-validation-2026-10-07.json) 的 make verify、322 个前端测试及 10 项真实 owner 浏览器结果。
- v0.34.8 失败任务明确为 `Release E2E gate / Playwright admin smoke`；Compose、MySQL/SQLite routing 和真实 owner IAM 浏览器门禁通过，镜像／Release 发布步骤跳过，见 [失败流水线](https://github.com/mengbin92/micro-one-api/actions/runs/37625675751)。
- 现网无副作用 `CreatePaymentOrder` 探针已解除覆盖拦截，服务健康和静态摘要已验证；没有据此声称创建真实生产订单或完成真实支付回调。

## 完整变更日志

包含 v0.34.8 候选已提交的业务修复与生产记录：

- `fix(iam): restore routing availability and subscription purchases`
- `docs(ops): record IAM self-service hotfix rollout`
- `docs(release): v0.34.8`
- `test(web): align authorization refresh smoke with retained actions`
- `docs(release): v0.34.9`
