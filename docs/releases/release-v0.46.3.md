# Micro-One API v0.46.3 发布公告

> 2026-11-19 · 上一版 [v0.46.2](./release-v0.46.2.md) · [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.46.3)

v0.46.3 是 **PATCH 修复版本**。本版修复订阅到期与续期的并发冲突、语音流式结算、管理台二次验证后的写入重试，以及通知、日志和 Redis 消费组的停机与恢复行为。

## 1. 订阅续期、倍率和对账

**根因**。到期扫描可以覆盖并发续期。管理端修改单个分组倍率时，会用先前读取的整表覆盖并发修改。订阅窗口对账使用当前倍率重算历史扣费，结算恢复查询也可能在服务停止后继续等待。

**修复**。到期、续期和倍率写入在各自的事务内读取并更新当前状态。窗口对账读取冻结的实际扣费，历史跨策略窗口确实缺少归属证据时标记为部分可核对。异步结算恢复使用可取消的上下文，停止与接收结算任务时同步处理队列状态。

**影响服务**。Admin API、Billing Service。

## 2. 流式语音识别与 WebSocket 停机

**根因**。流式语音识别按单行解析终态，截断流可能提前结算，多行终态也可能漏判。部分 WebSocket 连接在收到首帧后才登记，停机时会漏掉已经升级的空闲连接。

**修复**。完整终态事件才能触发结算，截断流释放预留。用量日志补齐预留和请求尝试标识。WebSocket 在握手后立即登记，排空开始后拒绝新连接，截止时关闭剩余连接。

**影响服务**。Relay Gateway。

## 3. 管理台二次验证和操作审计

**根因**。二次验证后的重试会遇到已经序列化的正文，原字段可能被覆盖。审计的计数与分页读取分开执行，并发写入时可能出现结果不一致。页面按总数判断下一页，可能进入超出查询窗口的页码。

**修复**。重试保留原 JSON 字段、资源版本和审计原因。审计各所有者在同一只读快照内读取总数与记录。服务端返回 `has_next_page`，管理台到达查询窗口上限时提示缩小筛选范围。

**影响服务**。Admin API、Billing Service、Channel Service、Config Service、Log Service、Monitor Worker、Notify Worker 和 Web 管理台。

## 4. 通知投递、事件流和异步日志

**根因**。通知的过期租约只在启动时回收，提前认领一整批记录也可能耗尽后续记录的租约。Redis 消费组丢失后消费可能停止，退出的 Relay 实例留下的组还会阻塞裁剪。异步日志写入与请求方共享可变记录。

**修复**。通知每轮回收过期租约，发送前逐条认领，停机时等待已确认的发送结果落库。Redis 消费组缺失时自动重建，每个主题只启动一套消费循环，共用进程内重领去重。新 Relay 临时组带租约，正常退出时清理，异常退出的组在租约过期后由后续发布清理。异步日志队列持有独立记录副本。

**影响服务**。Notify Worker、Relay Gateway、Log Service。

## 5. 分页、健康探针、迁移与部署工具

**根因**。大页码偏移可能在 32 位计算中溢出，内存列表顺序也可能不稳定。部分内存仓储存在复合键碰撞、删除后 ID 重用或共享切片。存量库迁移基线逐条登记，失败后可能留下部分记录。健康探针还可能将 JSON `null` 视为有效模型列表，部署失败也可能留下临时镜像包。

**修复**。分页以 64 位计算偏移并使用稳定排序。内存仓储用结构化配置键和单调 ID，渠道健康记录返回独立副本。基线登记整体提交或回滚。健康探针要求模型列表字段为数组，部署脚本在退出时清理临时镜像包。

**影响服务**。Identity Service、Channel Service、Config Service、Billing Service、Log Service、Monitor Worker、Notify Worker，以及迁移和部署工具。

## 兼容性说明

- API 和数据库沿用 v0.46.2 契约，无新增迁移。操作审计响应兼容新增 `has_next_page`，旧版前端仍可读取原有字段。
- 本版未引入新的部署配置项。历史 Relay 消费组没有租约，核实旧实例已退出后再清理，详见[路由失效传播](../design/group-redesign-v2-phase-d.md#4-事务-outbox-与失效传播)。

## 升级步骤

1. 部署窗口开始前备份当前配置，记录当前镜像版本并保留回滚标签。曾中断存量库基线初始化的环境先核对 `schema_migrations`。
2. 升级时固定使用 `v0.46.3` 镜像。Web 管理台按部署形态单独更新静态产物。操作步骤见[部署手册](../deployment.md)。
3. 升级验收覆盖订阅续期和对账、完整及截断语音流、二次验证后的管理写入、审计翻页、通知恢复和 Redis 中断恢复。
4. 回滚时恢复上一版镜像与对应 Web 静态产物。

## 验证

- [x] 本地 Go 构建、vet、单测、集成、race、架构和 RBAC 校验通过。
- [x] SQLite 隔离 Compose 全阶段通过。审计快照与冻结扣费对账在 SQLite、MySQL、PostgreSQL 验证。
- [x] 前端 807 项单测通过。Playwright 桌面与移动 121 项通过、1 项按配置跳过，另有 28 项 CSP 和 10 项真实 IAM 浏览器用例通过。
- [x] 自建网关 OCR 覆盖全部 46 个可审查文件，复核后 P0/P1 均为零。
- [x] 2026-11-20 生产部分交付及烟测。Identity、Channel、Config、Log、Monitor、Admin 和同源 Channel worker 保留 v0.46.3 镜像，Web 公网入口摘要与本地制品一致。Relay、Notify 沿用 `09ad6874`。公网健康和状态为 200，匿名操作审计为 401，已留 Compose、镜像和 Web 回滚快照。
- [x] Billing 生产热修复。原启动对账 #1448 因排序规则冲突新增 6 次 MySQL 1267。单独回滚后 #1449 恢复 `completed`。修正的新 JOIN 经生产只读 `EXPLAIN` 验证，冻结明细仍用主键 `eq_ref`。三库混合排序规则回归、Billing 全包测试及 vet 通过。11:16 单独部署 v0.46.3 加本补丁的 linux/amd64 制品 `v0.46.3-recon-19601c2c5aca`，自然启动对账 #1450 为 `completed`，新增 1267 为零，既有 `ledger_log_consume`、`receivable_mirror` 两条差异与 #1449 相同。健康检查通过，其他服务及 Web 未切换。
- [ ] 生产真实业务验收。续期与窗口对账、完整/截断语音流、二次验证后的管理写和审计翻页、通知与 Redis 恢复待补真实样本。隔离环境回归与上线烟测已有记录。

- [x] 通用发行前验证 MySQL 新装执行计划。旧热修复固定使用 `utf8mb4_unicode_ci`，新装库默认 `utf8mb4_0900_ai_ci` 时复现冻结明细全表扫描。后续修复从列元数据确定比较规则，排序规则相同直接比较，混合时仅调整预扣 ID 一侧。隔离 MySQL 8.4.11 两种布局均使用 `PRIMARY / eq_ref`，SQLite、PostgreSQL 及 MySQL 金额回归通过。后续修复复用现有表结构，处于本地验证状态。Billing 在进程内复用成功读取的排序规则，手动修改相关列的排序规则后需重启 Billing 以刷新。

## 完整变更日志

- `docs: record production Realtime acceptance`
- `fix(admin): update routing group ratios atomically`
- `fix(subscription): serialize expiry with renewal`
- `fix(relay): settle only complete speech events`
- `fix(billing): reconcile frozen subscription charges`
- `fix(billing): cancel settlement recovery on shutdown`
- `fix(web): preserve managed write data after step-up`
- `fix(audit): read counts and records from one snapshot`
- `fix(admin): stop audit pagination at its query window`
- `fix(notify): recover delivery leases and persist acknowledgments`
- `fix(relay): track websocket sessions before the first frame`
- `fix(events): recover and retire relay consumer groups`
- `fix(data): isolate in-memory repository keys and state`
- `fix(data): keep pagination stable at large offsets`
- `fix(log): detach queued log records from callers`
- `fix(migrate): record brownfield baselines atomically`
- `fix(deploy): clean temporary image archives on failure`
- `fix(monitor): require an array in model probe responses`
- `docs(changelog): record repository review fixes`
- `docs(release): v0.46.3`
