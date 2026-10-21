# Micro-One-API v0.36.3 发布：最大计时摘要内存修复与供应商证据详情

> 2026-10-21 · 上一版：[v0.36.2](./release-v0.36.2.md)（2026-10-19）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.36.3)

v0.36.3 是 v0.36.2 之后的 **PATCH 修复版本**：限制最大合法计时摘要的 SQL 语句内存，补齐现有供应商费用核验页的证据详情与撤权清理，并将版本化 Jaeger 内存配置同步为 768 MiB。无新增迁移、proto 或公开 API 变更；运行时影响 `log-service`、独立前端与 Jaeger 部署配置。

本批代码已于 **2026-10-20** 按 Log → Relay → 前端顺序部署生产；本次发布版本制品，不重复部署或重启生产实例。原 48 小时观察的 FAIL/PAUSED 结论与 TTL、WS、自然业务峰值等独立验收边界保持。

## 修复与改进

### 1. 最大计时摘要按语句预算写入，保持同组事务

**根因**：128 条最大合法摘要会在一条 INSERT 中携带约 47 MiB JSON；PostgreSQL 参数编码与 Bind 缓冲将实测峰值推至约 633 MiB，超过预先固定的 512 MiB 验收预算。

**修复**：在同一事务内按每条 INSERT 最多 4 MiB 计时 JSON 拆分，普通小摘要沿用原写入路径；关联引用每条语句最多 1,024 行、5,120 个参数，兼容 SQLite、MySQL、PostgreSQL。保留 128 份快照、事务原子性、去重、2 秒事务期限，以及 Relay 的 4,096 队列和 256 worker。新增最大输入、回滚、RPC 重放与隔离内存回归，不提高既有容量门槛。

**影响服务**：`log-service`；Relay 最大队列占用同时完成隔离验证。本版不修改 Relay 导出策略，SQL JSON 预算也不等同于服务总内存上限。

### 2. 供应商费用核验页补齐只读证据与撤权清理

**根因**：既有报告详情未完整呈现 owner 已返回的完整性、水位、来源映射及更正证据，金额缺失与零值需要保持区别，授权变化后已选详情也必须清除。

**修复**：复用现有接口展示报告及分页完整性、双方水位、来源映射、覆盖/舍入、用途及未知记录、金额差异、归因和更正前序/差额。金额保留十进制字符串；null/缺失与 0 分开。工作区绑定授权 generation，撤权清除选中报告，恢复授权不自动重开旧详情。核验结论仍采用 owner 返回值。

**影响服务**：独立前端；无需重建或重启 `admin-api`。没有新增 API、权限、报告历史浏览或写入动作。

### 3. Jaeger 配置保留已确认的 768 MiB 预算

**根因**：生产已由维护者调大至 768 MiB，仓库 Compose 仍为 512 MiB，后续重建可能恢复旧值。

**修复**：将版本化 Compose 同步为 768 MiB，并验证配置展开与部署引用。

**影响服务**：Jaeger 部署配置。生产已处于该预算，本轮上线未重启 Jaeger；该预算不改变 Relay/Log 的内存配置，也不代表原观察通过。

## 兼容性说明

- 相对 v0.36.2 无新增 schema、proto、公开 API、依赖或业务权限；更早版本须先完成对应迁移。Log 139 是 v0.36.2 的索引迁移，本次生产部署时已补齐。
- Log 继续兼容旧 Relay，事务、claim/refs 关联、重放去重与超时语义保持；过载导出仍为有界、尽力交付。
- 前端由宿主机 `/opt/web/dist` 挂载提供，升级后端镜像不会更新前端。
- 不修改钱包、价格、供应商结算结论或 executor 灰度范围。回退应用可保留兼容索引，不自动恢复数据库或 outbox。

## 升级步骤

1. 已于 2026-10-20 完成本批生产部署的实例无需重复部署；源码、镜像、前端哈希及回退点见[生产部署记录](../runbooks/p1-production-deployment-2026-10-20.md)。
2. 其他环境先备份配置、数据库和原前端；确认此前版本的迁移已完成。从 v0.36.2 升级无需新增迁移，更早版本需确认 Log 139 等既有迁移。
3. 按仓库部署流程本地交叉构建 Linux/amd64 镜像，先更新 Log 并验证摘要写入，再按实际版本差异更新 Relay。禁止在资源受限生产主机上构建镜像。
4. 构建并独立发布 `web/dist`，保留旧哈希资产并最后替换 index；无需重启 Admin。核对供应商报告权限、金额及缺失值展示和撤权清理。
5. 确认 Compose 中 Jaeger 为 768 MiB；若目标环境需要重建，单独记录新实例边界。检查服务健康、摘要/claim/refs、失败与丢弃计数，继续独立验收 TTL、WS 和自然业务峰值。
6. 需要回退时恢复备份的兼容镜像、前端及配置；不自动回退业务数据库或 outbox，避免覆盖部署后的写入。

## 验证

- 最大合法摘要包含 128 attempts、22 stages、32 reasons，独立字符串覆盖 HTML 转义扩张；JSON 368,407 B、protobuf 69,409 B。11 项越界拒绝、往返、事务回滚与持久去重通过。
- 独立 Linux/amd64、Go 1.27.2、各进程 4 CPU / 1 GiB：Relay 4,352 项最大摘要三轮饱和/排空 RSS 峰值 740.07 MiB，低于预定 768 MiB；拆分后 PostgreSQL 最大批次 197.70 MiB；三库最大输入 RPC 峰值 291.51 / 280.41 / 389.04 MiB，均低于 512 MiB。预算仅覆盖该隔离剖面，生产 Relay/Log 原有未设置容器硬上限的配置保持。
- [原生三库容量 workflow](https://github.com/mengbin92/micro-one-api/actions/runs/39397036764) 完整 SUCCESS：原 64 并发、四组交替、每模式 30 秒、十轮 race 和暂停探针保持；447,831 条摘要、5,376,132 个完成及接收 span，关联一致，错误/失败/丢弃为 0，最高 P95 增量 4.6499%，未放宽 5% 门槛。历史失败与原始证据保留，详见[P1 实施记录](../runbooks/p1-memory-vendor-evidence-2026-10-20.md)。
- 前端全量 573 项测试通过，页面专项 9 项与全量有重叠；lint、类型检查、构建及六项资产预算通过。中英文 × 明暗主题 × 桌面/手机八项隔离浏览器验收通过，未将 fixture 验收表述为生产真实账期登录验证。
- 四种 Compose 组合、41 项 Kubernetes schema、83 项引用检查通过；合入 develop 的 [CI](https://github.com/mengbin92/micro-one-api/actions/runs/39421417661) 与[安全检查](https://github.com/mengbin92/micro-one-api/actions/runs/39421417648) 全部 SUCCESS，之后仅新增发布文档。
- 2026-10-20 生产冒烟：九服务健康、重启 0、OOM false；旧/新 Relay → 新 Log 的未认证探针均 HTTP 401 且摘要落库；前端入口及详情 chunk 哈希匹配，Jaeger 原实例保持 768 MiB。未调用真实付费模型。
- tag 推送后由 Release workflow 执行完整 E2E、九服务双架构构建、平台清单校验并发布 GitHub Release；其最终结果以 workflow 为准。生产 TTL、WS、长期容量和原 48h 观察不由本次发版代替验收。

## 完整变更日志

- `feat(admin): expose supplier report evidence details`
- `fix(timing): bound SQL buffers for maximum summaries`
- `fix(deploy): retain the confirmed Jaeger memory budget`
- `docs: record verified P1 memory and supplier evidence delivery`
- `docs: record verified P1 production deployment`
- `docs(release): v0.36.3`
