# 分组重设计 v2：阶段 A 实施记录

> 2026-09-08 · 分支 `codex/group-concepts-refactor` · 基于 `efacbf7c` 工作树
> 状态：已完成；工具、本地验证及经授权的真实基线清点均已通过，阶段 B 可进入。
> 主方案与各阶段状态：[group-redesign-v2.md §8.2](./group-redesign-v2.md#82-实施阶段与上线闸门)。

## 已实现

`group-audit` 输出升级为 `version: 2`，继续使用 channel 的生产 `CanRoute` 查询，并保持单个只读快照。没有新增迁移、回填或运行期开关。

| 内容 | 实现与兼容行为 |
|---|---|
| 用户 / Key | 原有用户组引用；新增 Token ID 与 user ID；建议默认组 + migration 授权、`explicit_only` 和 Key `inherit` |
| 资源 / 模型 | 保留渠道、账号、abilities、模型映射、路由规则和有效组/模型/来源矩阵；同数字 ID 的两类来源继续分开 |
| 有效价格 | 纳入 billing 基础配置独有的组键；保持动态映射整体替换、无效倍率过滤、缺键回退 1 的当前语义；无实际基础输入时阻止回填 |
| 订阅引用 | 新增额度策略的限额/倍率、套餐引用、用户订阅期限/状态、订阅类订单及其快照数字引用；涵盖待支付、关闭及已支付未履约等历史状态 |
| 迁移建议 | 有路由引用的组建议 enabled / restricted / subscription_first；只有价格的组建议 disabled；旧异常键原样保留，不小写、不截断 |
| 权益边界 | 套餐、订阅、旧订单统一建议 `legacy_all_authorized`，不授予路由访问；旧额度策略保持 `legacy_live`，数字 quota policy ID 不加入路由组集合 |
| 诊断 | 每个 issue 增加 `action`、`blocking`；`migration.ready_for_backfill` 只在无阻断项时为 true，不代表执行过回填 |

PostgreSQL 支持新增 `postgres` driver，并将原硬编码反引号的 `group` 列引用改为驱动引用。MySQL/SQLite 的谓词与参数保持不变，仍记录各数据库当前排序规则和 LIKE 行为；精确成员语义的切换属于阶段 B。

## 异常处置

| 发现 | 处置 |
|---|---|
| 模型授权在账号 CSV 之外 | 保留模型级映射，不新增账号全模型成员关系 |
| 展示价与实际价不同 | 冻结实际 billing 倍率及来源，不使用页面默认展示值 |
| 重复成员 | 按现有解析器的精确成员去重，不增加候选权重 |
| 价格独有键 | 保留草稿及价格，不产生资源或用户资格 |
| 旧订单没有快照 | 保持旧履约语义，不按当前套餐名称猜测覆盖组 |
| 大小写/空白冲突、`%` / `_` 等敏感键、无精确引用的实际授权 | 标记阻断；隔离并核对实际有效候选后决定映射，不静默修复 |
| 悬空用户/额度策略/订阅引用、无效倍率、重叠 active 行、异常期限、损坏或冲突订单快照 | 标记阻断；恢复原合同或引用关系后重跑清点 |

无限模型通配集合仍只能保留规则并通过 `--model` 补充具体探针；报告不声称覆盖全部未来模型。订阅是否在某一时刻有效需结合报告中的 starts_at、expires_at、status 判断，不依赖扫描任务或报告生成时刻。

## 验证

| 检查 | 结果 |
|---|---|
| `make test-unit` | 通过；包含仓库 proto 生成及默认后端测试 |
| `./scripts/check-architecture.sh` | 通过 |
| 定向 `go test -race` | channel biz、data 与 group-audit 通过 |
| SQLite | 无凭据列的合成快照；报告确定性、文件不变、只读拒写、输出 0600、不覆盖旧报告、缺表/探针不足不输出部分基线 |
| MySQL 8.0 / PostgreSQL 16 | 本机隔离容器；独立 channel/identity/billing schema；完整 CLI 清点、重复输出一致、跨 schema 可重复读、只读拒写 |
| 授权边界 | 两类来源同 ID；模型级越组授权；MySQL 大小写差异；`%` / `_` LIKE 副作用；CSV 空白记录 |
| 合同与脱敏 | 旧订阅不生成路由授权；损坏快照仅输出诊断；报告和错误信息不包含原始快照内容或套餐名称 |
| Linux/amd64 构建 | 本机 Go 1.27.1 交叉编译成功；工作树实现尚未提交，`vcs.modified=true` |

首次仓库级测试和架构检查因沙箱无法访问 Go 构建缓存而中止；允许缓存访问后重跑通过，不将首次失败记为代码通过。

真实引擎测试通过以下环境变量显式启用，默认单元测试跳过外部数据库。只能指向临时测试实例；测试创建并清理唯一名称的 schema，需要 CREATE/DROP 权限：

```sh
# DSN 由测试环境注入；PostgreSQL 测试 DSN 使用 postgres:// URL 格式。
GROUP_AUDIT_TEST_MYSQL_DSN="$TEST_MYSQL_DSN" \
GROUP_AUDIT_TEST_POSTGRES_DSN="$TEST_POSTGRES_DSN" \
go test -race -count=1 ./app/channel/cmd/group-audit \
  ./app/channel/internal/data ./app/channel/internal/biz
```

这些验证只涵盖清点工具和所复用的现有查询，不代表阶段 B 的成员表、回填、双写、事务重建或新旧候选影子比较已验证，也不代表 Relay/计费/前端改造已经完成。

## 已完成：真实基线

本次已通过既有 SSH 配置，只读核验 billing 的基础分组倍率和配置视图来源。这些元数据检查没有与完整清点共用快照，不能替代基线。

用户已明确授权列明范围的生产只读清点及本机导出，先前的自动审批阻断已解除。2026-09-08 使用当前工作树交叉编译的 Linux/amd64 审计程序执行两次独立只读、可重复读事务，报告逐字节一致，`migration.ready_for_backfill=true`，阻断项为 0。

实际操作范围：

- 来源：既有部署主机上的 MySQL，channel / identity / billing 所有者表与 billing 配置视图；一次只读、可重复读事务。
- 内容：用户/Key 的数字 ID 与组引用、资源与模型授权、价格倍率、额度策略限额、套餐/订阅/订单的引用 ID 与生命周期状态。报告不含用户名、邮箱、渠道 Key、账号凭据、支付流水、支付 URL 或原始订单快照。
- 目的地：本机仓库的 `artifacts/group-audit/2026-09-08/`，已受 Git 忽略规则保护；报告文件 `0600`，不覆盖旧文件。它仍属于内部业务数据，不提交 Git。
- 执行方式：在本机交叉编译临时审计程序，远端运行后清理；不部署服务、不迁移数据库、不修改生产数据。

三项非阻断发现均为价格来源差异：两项账户展示倍率与实际 billing 倍率不一致，一项资源组没有动态价格覆盖。处置统一为冻结实际 billing 基础倍率及其来源；有资源/用户引用的组保留启用、专属访问与订阅优先语义，只有基础价格引用的组建立禁用草稿，不自动授予使用资格。旧用户与 Key 按 migration 授权 / inherit 迁移，旧订阅按 legacy_all_authorized / legacy_live 保留，不猜测路由覆盖范围。

私有报告为同目录 `baseline-v2.json`、`baseline-v2-repeat.json`；`build-manifest.json` 保存 HEAD、工作树源文件哈希、二进制哈希和 Go 构建信息，`summary.md` 保存内部明细及处置。文件均为 `0600`，受 Git 忽略规则保护；真实数据不提交 Git。无部署、无生产数据修改、无迁移，远端临时程序执行后清理。

原 2026-09-07 报告在当前本机目录中不可用，因此仅确认本次两份报告一致，不声称与旧日报告做过逐条比较。阶段 A 完成不表示阶段 B 的迁移已经执行；后续上线前如源数据变化，仍需刷新清点并处理新阻断项。
