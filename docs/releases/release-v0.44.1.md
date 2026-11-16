# Micro-One API v0.44.1 发布：结算恢复授权、路由迁移与跨库操作审计修复

> 2026-11-16 · 上一版：[v0.44.0](./release-v0.44.0.md)（2026-11-15）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.44.1)

v0.44.1 是 v0.44.0 之后的 **PATCH 修复版本**，收口最近三个 MINOR 的 code review：修复 relay 结算恢复的服务身份拒绝、迁移 151 的旧路由回填与重放边界、订阅账号并发建模失败，以及操作审计跨 owner 漏读、共享库重复计数和用户名补充超时。

**版本判 PATCH 而非 MINOR 的依据**：本次恢复已有结算、路由与审计功能的正确行为，没有新增面向用户的功能或 HTTP 入口。六个 owner 的内部只读 RPC 用于补齐现有操作审计读取链路；其新增字段与部署接线不改变用户功能范围。**无新增迁移编号、无前端改动、无计费规则变更**。

## 修复内容

### 1. 结算恢复的服务身份授权

**根因**：v0.44.0 的 relay commit 补偿通过 billing `ListRequestAttempts` 查询已持久化状态，但该 RPC 原先只允许 admin 用户代理。真实服务身份拦截器会拒绝 relay，恢复读取退回 `commit_error`，已成功扣款请求无法继续补齐用量和来源记账。

**修复**：为该 RPC 明确授予 relay 专用 system capability，打通 transport → biz → data 的权威结算读取；继续保留用户代理的 IAM 与成本字段授权边界。测试覆盖真实拦截器与 billing biz 的权限链，不通过给 relay 伪造用户权限放行。

**影响服务**：`billing-service`。若 relay-gateway 已运行 v0.44.0 的重试与恢复代码，本项只需更新 billing 的编译内权限契约；旧 relay 需先升级到包含恢复逻辑的版本。

### 2. 迁移 151 的回填与重放边界

**根因**：切换窗口内，新代码更新来源当前模型/分组清单后，旧投影可能仍保留被删除路由。迁移直接回填会将其复活；MySQL 在 DROP 已提交而版本登记失败时，重跑又会读取已不存在的旧表。

**修复**：

- 三方言按父资源当前 `models` 清单校验旧投影；订阅路由同时校验当前 `group`，不回填已移除的模型或分组。
- CSV 只去除 token 边界空白，保留合法内部空格与分组大小写；SQLite 使用字面 token 比较，`_`、`%` 不再成为通配符。
- MySQL 对所有读取旧表的回填段增加源表存在性守卫，允许 DROP 后版本未登记的失败运行安全重放；保留 PostgreSQL/SQLite runner 的事务边界。

**影响服务**：尚未应用 `151_retire_legacy_abilities` 的 channel 数据库及迁移任务。**已经登记 151 的线上数据库不重跑，不删除迁移 marker**；本版不自动回写既有映射。

### 3. 订阅账号并发创建模型

**根因**：PostgreSQL 唯一键冲突会将当前事务置为失败，捕获后在同一事务重查仍返回 `25P02`；MySQL 的旧快照与默认 `clientFoundRows=true` 又可能把冲突 no-op 当成插入成功，返回模型 ID 0。

**修复**：使用 `OnConflict DoNothing` 争抢插入，以返回的非零模型 ID 判断插入结果；否则锁定读取获胜行，保留事务、权限和错误处理。双库回归通过同步屏障复现并发冲突，使用正式连接配置验证 `clientFoundRows` 边界。

**影响服务**：`channel-service` 与复用同一镜像的 `channel-account-ops-worker`。

### 4. 操作审计完整性与可用性

**根因**：初版只读 admin schema，漏掉其他 owner 的事务审计。直接汇总多个来源时，共享数据库又会重复计数；每个 actor 独立使用 2s 超时会把整页用户名补充拖过请求期限。

**修复**：

- admin 聚合本地与 channel/billing/config/log/monitor/notify 六个 owner 的只读审计源。owner RPC 只允许 admin 专用 system identity；对外入口仍要求 global `iam.audit.read`，RPC 调用前剥离用户 operator 凭据。
- 各源返回实际物理审计库的非空哈希身份，聚合按库去重记录和 total；来源不可用、缺少身份或返回不完整批次时明确失败，不返回误导的半份数据。
- 各源只读取当前全局页所需前缀，按 `occurred_at DESC, event_id DESC` 统一排序；超过全局前 5000 条窗口返回 400，提示缩小时间范围或筛选。
- 整页用户名补充共享一个 2s 预算，耗尽即返回已有数据并降级为数字 ID。memory 模式缺少审计存储时返回不可用错误，避免 nil DB panic。
- MySQL/PostgreSQL/lite Compose 与 Kubernetes 补齐审计 gRPC endpoint、ConfigMap/env 和限定目标 Pod/TCP 端口的网络许可；Proto 注释明确毫秒时间、0-based 内部分页、排序、完整 total 与 StoreID 契约。

**影响服务**：`admin-api`、`channel-service`、`billing-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker`。

## 兼容性说明

- 现有公共 HTTP 入口和计费规则不变；审计超窗请求改为明确的 400，避免将截断结果当成完整记录。
- admin-api 与六个远端 owner 必须升级到包含本修复的同一版本；旧 owner 缺少读取 RPC 或 StoreID 会明确失败。
- admin 必须使用专用服务凭据，各 owner 必须信任该身份；共享服务 token 不授予审计 system capability。真实凭据由部署环境提供，仓库不生成或记录。
- 无新增迁移编号。151 的修正版只供尚未应用该版本或版本登记失败的环境使用；已经成功应用的环境保留原 marker，不手工重放。
- 本版没有前端变更，不需要更新宿主机 `web/dist`；不改变 orchestrator 全量开关、灰度 allowlist 或观察窗口状态。

## 升级步骤

1. 确认本地与远端版本、部署目标及生产 151 状态。目标从 `.env` 的 `DEPLOY_REMOTE_SERVER` / `DEPLOY_REMOTE_DIR` 读取；已登记 151 时跳过迁移。
2. 为即将替换的服务镜像打 rollback 标签，并备份生产 Compose；尚未应用 151 的环境先按 [v0.43.0 升级步骤](./release-v0.43.0.md#升级步骤)备份数据库，使用本版修正后的迁移文件。
3. 核对 admin 专用服务身份、六个 owner 的受信身份配置，以及 LOG/CONFIG/MONITOR/NOTIFY 四个 gRPC endpoint；Kubernetes 同时核对 ConfigMap 引用和精确网络许可。
4. 本地交叉构建 linux/amd64 并部署 `billing-service`、`channel-service`、`config-service`、`log-service`、`monitor-worker`、`notify-worker`、`admin-api`；不得在资源受限的生产机器上构建。先更新 owner，再更新 admin，避免新 admin 访问旧 RPC。
5. 在现有 `account-ops-shards` profile 下，将 `channel-account-ops-worker` 同步切换为本次 channel-service 的同一镜像 tag；仅更新具名 channel-service 不会自动更新复用镜像的 worker。
6. 已运行 v0.44.0 恢复代码的 relay-gateway可保留原镜像；核验 billing 已包含 relay system capability。若现网 relay 版本更旧，再与 billing 一并更新。保持既有 `RELAY_ORCHESTRATOR_FULL_ROLLOUT` 设置。
7. 核验容器状态、重启次数、健康端点与日志；使用获授权账号查询跨 owner 审计，验证 global 权限、排序、共享库不重复计数及故障显式失败。结算恢复验收应使用受控测试，未经授权不制造真实生产账务故障。

**回滚**：按本次预先保存的镜像标签回退 admin 与 owner 服务，并同步回退复用 channel 镜像的 worker；没有新增迁移需要逆向执行。已经应用 151 的数据库继续保留 marker，不恢复已删除旧表、不重放迁移；若从 v0.43.0 之前的 schema 升级，则沿用该版本的数据库备份恢复边界。

## 验证

本地证据见[本轮修复记录](../runbooks/code-review-fixes-v0.42-v0.44-2026-11-15.md)，以下结果不替代发布 CI 或生产验收：

- [x] 非 E2E、非外部 integration Go 单测：130 个有测试包通过；相关 race：50 个有测试包通过；`go vet ./...` 通过。
- [x] 七个 data 源与 StoreID 的真实 MySQL/PostgreSQL 整合：8 包、57 个用例/子用例通过；迁移 151 三方言 Runner 边界回归通过。
- [x] config/log/monitor/notify 真实 gRPC 权限、存储与错误链回归；审计前端页面/导航/路由 3 文件、40 测试通过。
- [x] Proto/Wire 由既有生成命令刷新；RBAC 974 项匹配；Compose 四种组合、Kubernetes 渲染/89 项引用/精确 egress 静态检查通过。
- [x] `go build ./...` 与 `make verify` 全绿，含 756 项前端全量测试、196 项 critical tests、lint/typecheck/build；7 个 linux/amd64 候选镜像构建并核对源码摘要。
- [x] 按项目允许的 OCR delegate 模式补齐未完成/后续变更的 35 项，加上已完成且未变的 27 项，共 62 项逐路径 hash 核验，P0/P1 为 0。外部模型两轮结果仍为 partial/failed，不冒充其通过；[门禁证据](../runbooks/evidence/release-v0.44.1-gates.json)。
- [x] 临时使用官方固定版本验证工具完成完整部署检查：kubeconform 42 个资源 valid，0 invalid/error/skip；4 组 Compose、89 项引用与 routing gates 全绿。
- [ ] develop/main CI 与 Release：待发布后回填。
- [ ] 生产部署、健康检查、跨 owner 审计和结算恢复验收：待执行后回填。

## 完整变更日志

本轮代码按职责拆为三条 Conventional Commit：

- `08d8a2c3` fix(billing): authorize relay settlement recovery
- `b3864ac5` fix(channel): make registry migration and concurrent creation safe
- `1ecc47e6` fix(admin): complete cross-owner operation audit reads
- `docs(release): v0.44.1`

发布后完整范围：`git log v0.44.0..v0.44.1`。
