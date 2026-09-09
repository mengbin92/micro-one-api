# 分组重设计 v2 · 阶段 B 完成记录

> 日期：2026-09-09
> 状态：本地实现与验证完成；未执行生产迁移、回填、部署或运行路径切换。
> 方案：[group-redesign-v2.md](./group-redesign-v2.md) §8.2。

## 1. 交付范围

channel 新增稳定的路由分组 ID、两类资源成员表、模型映射/模型路由的数字组引用，以及按报告摘要记录的回填进度。迁移 `090_create_routing_groups.sql` 有 MySQL、PostgreSQL、SQLite 三个版本，所有权归 channel。

- `routing_groups.key` 按原始字节精确唯一；MySQL 使用 `VARBINARY(1024)`，PostgreSQL 使用 `COLLATE "C"`，SQLite 使用二进制排序规则。大小写、百分号、下划线及尾部空白不会在实体查询中合并；测试同时覆盖长中文旧键。回填支持最多 1024 字节的旧键，更长键明确拒绝，不截断。
- 旧资源 CSV 通过既有解析器生成去重的成员关系。账号的模型级额外授权只写入原映射的 `routing_group_id`，不补充账号成员关系。
- 原映射 ID、上游模型名、启用状态、优先级和资源权重保留；模型路由仍只收窄原候选。
- `group-backfill` 显式执行回填；进程启动不执行全量迁移。默认演练在事务中写入后回滚，`--apply` 才提交。
- channel 增加 `ListRoutingGroups` / `GetRoutingGroup`；admin 增加受管理员鉴权保护的只读 HTTP 列表与详情；前端增加“分组”导航及页面。

只读页面显示名称/键、状态、使用资格、两类资源成员、继承的优先级/权重和账号模型授权。额外授权有独立标记。价格、订阅权益、用户授权编辑及分组 CRUD 属于后续阶段；页面不把尚未接入的数据显示为零或已经生效的策略。

## 2. 回填与一致性闸门

命令要求完整 v2 清点报告、`ready_for_backfill=true`、零阻断项及有效的旧键映射。在同一个 serializable 事务中：

1. 重新读取清点涉及的事实，比较 channel 所有的资源、映射和模型路由引用，拒绝清点后的变更。
2. 按报告中的组、模型探针和当前两类资源重新计算旧 `CanRoute` 授权矩阵，要求与报告一致。
3. 按精确键插入缺失实体，保留已有 ID/元数据；重建成员关系，补全原模型映射和路由行的数字组引用。
4. 使用关系表读取的影子仓库调用同一个 `CanRoute`，比较允许的来源 kind/id 和上游模型名；任何差异回滚整个事务。
5. 写入报告 SHA-256、完成时间和组数。重放相同报告不会重复创建进度记录或重置实体元数据。

中断未提交的回填会整体回滚，可以重新执行；提交后重复执行保留实体 ID，也可以修复丢失的成员投影。本阶段使用单事务重建，不声称已经支持大规模分片回填。影子探针上限为 100,000 组×模型×来源组合；模型通配空间仍采用报告中明确列出的有限探针，不声称枚举无限模型名。

影子比较只检查授权，不进行选择后的健康调度，不调用 billing、不预扣、不累计用量。即使人为清除清点报告中的阻断标记，旧 CSV 的 LIKE 通配扩权仍会被重新比较阻断。

本阶段仅重验证 channel 事实；A 报告中的用户、订阅、价格不是 channel 写入对象。进入 C/E 的实际迁移前必须由各所有者再次验证对应基线，不能用 B 成功替代后续闸门。

## 3. 双写与发布边界

`CHANNEL_ROUTING_GROUP_DUAL_WRITE=true` 开启 channel 的兼容双写，默认关闭。启动检查新增表、两类映射的列和至少一条成功回填记录，未准备好时拒绝启动双写模式。

- 渠道与账号原有创建/更新路径在同一数据库事务中更新 CSV、成员关系和 abilities；删除资源同步清理关系。
- 模型映射的显式 upsert、模型导入和模型路由 upsert 同事务写旧组键与数字组引用；删除映射删除同一行，合并模型保留行上的数字组引用。
- CSV 编辑不能隐式创建组；引用未导入的旧键会使整个资源事务失败。新增组管理在后续阶段提供。
- 当前运行请求继续使用旧读取路径；关系表读取仅由回填影子比较使用，没有开放切换运行准入的环境开关。
- 组 revision 当前覆盖成员变更和映射 upsert；完整生命周期、模型/资源变更失效传播与 outbox 属于后续准入阶段，当前不得把它当作完整授权缓存版本。

部署步骤应在单独安排的维护窗口执行：先安装 additive schema，暂停 channel 资源/映射写入，刷新 A 报告，运行回填演练和提交，再让所有 channel 写入实例启用双写后恢复管理写入。保持旧写入实例在线会使投影漂移；不能仅开启部分实例后把回填结果当作持续一致。

以下为命令模板，不代表已对生产执行。DSN 使用环境变量，命令行不传入凭证：

```sh
# MYSQL 示例。先按现有迁移运行流程设置 MIGRATIONS_DSN。
go run ./cmd/migrate -driver=mysql -dir=./migrations -ownership=channel -status
go run ./cmd/migrate -driver=mysql -dir=./migrations -ownership=channel

# GROUP_BACKFILL_DSN 指向 channel 所有者数据库；报告来自刷新后的 group-audit。
# 同 schema 的本地库省略三个 schema 参数。
go run ./app/channel/cmd/group-backfill \
  --driver=mysql --report=/absolute/path/baseline-v2.json \
  --identity-schema=oneapi_identity --options-schema=oneapi_billing \
  --billing-schema=oneapi_billing

# 检查演练成功后，在相同写入暂停窗口显式提交。
go run ./app/channel/cmd/group-backfill \
  --driver=mysql --report=/absolute/path/baseline-v2.json \
  --identity-schema=oneapi_identity --options-schema=oneapi_billing \
  --billing-schema=oneapi_billing --apply
```

PostgreSQL 对应 `--driver=postgres` 和 `migrations/postgres`；SQLite 对应 `--driver=sqlite3` 和 `migrations/sqlite`，不使用跨 schema 参数。遵循仓库既有迁移所有权规则；生产镜像继续本地交叉构建，不在服务器构建。

演练会产生写锁，并可能消耗不回滚的自增序列值，**不是只读操作**。A 阶段的生产只读清点授权不代表已经执行或批准 B 的生产回填。失败后先检查进度和报告摘要，再重跑；不要在服务启动中调用命令。

在阶段 B，可关闭双写并回退旧版本，保留 additive 表及旧 CSV。回滚之后新增的旧写入需要重新清点/回填，再次启用前须重新验证；无需删除新表。

## 4. API 与分层

- channel service 解析 equality filtering、ordering 和 query-bound page token，调用 biz usecase，返回 proto DTO。默认页大小 50，最多 200，稳定追加 ID 排序。
- `filter` 支持 `key`、`status`、`access_mode` 的字符串等式及 `AND`；`order_by` 支持 `id`、`key`、`sort_order` 的 asc/desc。其他字段/操作、重复字段、错误 token 和不合法 ID 在 service 边界拒绝。
- admin biz 使用 `RoutingGroupReader`，由 `app/admin/internal/data/channelclient` 转换 channel RPC DTO 与共享 `domain/routing` DO。cmd Wire 注入；不跨服务读取 PO。
- 架构检查只为此窄适配包放行 `api/channel/v1`，未放宽其他 admin data 包的 DTO 依赖。
- HTTP：`GET /api/v1/admin/routing-groups` 和 `GET /api/v1/admin/routing-groups/{id}`。未登录拒绝访问；写方法返回 405；不存在/无效输入/依赖失败分别返回 404/400/503，错误不回显上游连接信息。

## 5. 验证记录

全部测试使用本机一次性数据库、临时 SQLite 和模拟 API；没有对生产写入测试数据。

| 检查 | 结果 |
|---|---|
| MySQL 8、PostgreSQL 16、SQLite 实际执行 090 | 通过 |
| MySQL/PostgreSQL 全迁移空库安装、重复迁移、状态审计及故意错误 SQL 闸门 | 通过 |
| SQLite 全量与增量升级 | 通过；升级测试固定在 084 之前播种旧价格，避免追加迁移移动测试边界 |
| 三方言旧/新候选比较、演练回滚、重复回填、稳定 ID、投影重建、基线过时拒绝 | 通过 |
| 三方言 abilities 写入故障的完整事务回滚、未知组拒绝、重复 CSV 去重 | 通过 |
| 三方言精确键唯一、大小写/空白/`%`/`_`、影子拒绝 LIKE 扩权 | 通过 |
| 模型导入失败回滚、额外授权不变账号成员、上游模型名/优先级/权重 | 通过 |
| 回填 CLI 演练/提交/重放、无效参数/尾随 JSON、诊断脱敏 | 通过 |
| service fake usecase、admin RPC adapter、HTTP 管理鉴权/只读限制、列表解析 | 通过 |
| `make all`、`make test-unit`、架构检查、`make migration-check` | 通过 |
| 相关 data/biz/CLI/API 与 admin 包 `go test -race` | 通过 |
| 前端 ESLint、TypeScript/Vite 构建、48 文件 / 172 测试 | 通过 |
| Chrome 桌面和移动视口 Playwright | 2/2 通过；截图人工检查，列表与详情无页面横向溢出 |

测试产物在本机 `/tmp/group-v2-b-*.log` 与 `/tmp/group-v2-b-{chrome,mobile-chrome}.png`，不作为生产证据。版本化的测试与本记录提供可重跑依据。

## 6. 下一阶段

阶段 C 补充 identity 默认组/授权事实，Relay 仍仅支持 inherit，billing 在预扣时冻结完整请求上下文并让同步/异步提交复用。原组金额、分账、Key 额度、会话及在途改价/换组均通过回归后，才能开放阶段 D 的固定组 Key。
