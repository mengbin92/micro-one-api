# 分组清点与统一路由授权

> 2026-09-07 · 分支 `codex/group-concepts-refactor`
> 上一阶段回滚点：`669d7af8`。概念定义见 [分组概念与实现方案](./group-concepts-and-implementation.md)。

## 授权规则与接入点

channel biz 提供 `CanRoute(group, model, source)`，source 用 `channel` / `subscription` 区分两个 ID 空间。
它回答当前模型是否授权给指定来源，并返回当前 `upstream_model_id`；健康、余额、额度与并发仍属于调度检查。

- 普通选择、排除来源后的选择，与指定来源检查共用能力查询及模型路由过滤。
- 注册表中的模型沿用注册表优先规则：没有启用的映射就是拒绝，不退回 legacy 或 catch-all。
- 未注册模型沿用 legacy 精确优先、再匹配通配符的规则；普通选择原有的非限制渠道回退保持不变，排除选择仍不扩大到 catch-all。
- `model_subscription_mapping.group_name` 是模型级授权，可以不同于账号 CSV。它不能授权该账号的其他模型。
- `model_routings` 只收窄可选账号，不创造授权；读取规则失败会返回错误，停止选择，避免绕过限制。
- 模型目录对原有公开模型列表再按同一规则过滤；修改模型路由时清理目录缓存。
- 缓存渠道命中、HTTP sticky、Responses / WebSocket 恢复重新查询权限并更新上游模型映射。
- 重试预先选出的候选和同一来源重试，在再次发送之前检查权限；检查失败时终止重试，不把这次本地拒绝记成上游健康失败。
- `RelayPlan.ClientModel` 保留全局别名解析前的授权键，检查顺序与正常选路一致：先客户模型，明确拒绝后再试全局别名；查询错误不触发别名回退。

Responses 本地缓存也需要用户归属、Token 模型权限及当前来源授权。缺失模型的历史路由无法证明授权，不能复用。
本阶段检查恢复与重试边界；已建立的 WebSocket 连接不会在每条数据帧上重新查询权限。

内部 RPC 为 `ChannelService.CheckRoute`，仅返回权限和上游模型 ID，不返回凭据。发布时先更新 channel-service，
再更新 relay-gateway；旧 channel-service 不实现这个 RPC，新 gateway 的复用检查会失败关闭。
回滚 gateway 后可继续使用新版 channel-service。本阶段没有数据库迁移。

## 只读清点命令

```sh
go build -o /tmp/group-audit ./app/channel/cmd/group-audit
# 通过环境安全注入 GROUP_AUDIT_DSN，不把带密码的 DSN 放进命令参数。
/tmp/group-audit --driver=mysql --output=/tmp/group-baseline.json

# 同一 MySQL 实例按服务拆 schema：DSN 指向 channel 拥有的数据库。
/tmp/group-audit --driver=mysql \
  --identity-schema=oneapi_identity --options-schema=oneapi_admin \
  --base-ratios-env=GROUP_AUDIT_BASE_RATIOS --output=/tmp/group-baseline-split.json

# SQLite 接受已有文件路径或 file:/绝对路径，工具强制 mode=ro。
GROUP_AUDIT_DSN=/absolute/path/snapshot.db /tmp/group-audit \
  --driver=sqlite3 --model=legacy-chat --output=/tmp/group-baseline-sqlite.json
```

MySQL 使用只读、可重复读事务；SQLite 使用只读文件连接和事务。命令不加载服务启动逻辑，不迁移表，
不执行数据修复，不读取用户姓名、邮箱、渠道 Key 或账号 Token。输出文件必须是新文件，权限为 `0600`。
查询日志关闭，连接和 SQL 错误不输出原始数据。数据库账号仍建议限制为 SELECT 权限。

清点要求同一快照内存在 `users`、`channels`、`subscription_accounts`、两类 abilities、`models`、
两类模型映射、`model_routings` 和 `system_options`。缺表、查询失败或超出限制时整体失败。
同一 MySQL 实例按服务拆 schema 时，通过 `--identity-schema` / `--options-schema` 指定两类跨服务表，
其余表使用 DSN 中的 channel 数据库。全部查询共用一个只读事务；底层表应使用 InnoDB，账号需具有对应 SELECT 权限。
schema 参数仅接受字母、数字、下划线构成的标识符（首位不能为数字，最多 63 字符），由数据库驱动引用。
不支持跨 MySQL 实例拼接事务，也不为审计创建数据库或视图；SQLite 不接受 schema 覆盖。
仅支持 MySQL / SQLite；PostgreSQL 成员查询方言一致性尚待单独验证，命令会拒绝该 driver。

可选参数：

| 参数 | 用途 |
|---|---|
| `--dsn-env` | DSN 环境变量名，默认 `GROUP_AUDIT_DSN` |
| `--base-ratios-env` | billing 基础 `GroupRatios` JSON 的环境变量名 |
| `--identity-schema` | MySQL 用户表的 schema，默认 DSN 数据库 |
| `--options-schema` | MySQL 价格配置表的 schema，默认 DSN 数据库 |
| `--model` | 补充一个具体模型名，可重复，用于验证通配符规则 |
| `--max-probes` | 授权查询次数上限，默认 100000 |
| `--timeout` | 整次事务期限，默认 2 分钟 |
| `--output` | 新报告路径；省略则输出 stdout |

退出码：`0` 完成且没有发现项；`1` 完成且存在发现项；`2` 参数、读取、探测或输出失败。
发现项包含待确认的合法配置，例如模型级越组授权或未覆盖价格，不能把退出码 `1` 当成删除这些配置的依据。
使用编译后的命令可直接保留退出码；`go run` 会额外包装非零退出状态。

## 报告解释与基线边界

报告按固定顺序输出，没有运行时间戳，便于对同一快照做差异比较：

- `references` 保留用户单组、渠道/账号 CSV、能力、映射、模型路由及价格中的组键。abilities 的 `id` 使用 channel ID，其余记录使用所在表的 ID。
- `issues` 标记空值、空白、重复成员、单键中出现 CSV、大小写冲突、SQL 模式字符、失效来源/模型引用、模型级越组授权，以及资源/价格配置不匹配。
- `grants` 通过生产授权实现逐一探测得到，包含组、模型、来源和上游模型映射；来源记录可以包含禁用资源，授权结果只包含当前允许项。
- `prices` 对比管理列表、账户展示和推算的有效倍率。未提供 billing 基础配置时标为 `builtin_base_assumption`，不代表读取了线上 billing 配置。

有效倍率遵循当前 billing 规则：非空动态 `GroupRatio` 替换基础映射，过滤非正数和空键，缺失组回退 `1`；
没有动态覆盖时使用提供的基础配置或内置 `default=1/vip=0.5/svip=0.3`。这只是路由组价格倍率，
不包含订阅额度策略的消耗倍率，也不重算历史账本。

模型探测集合是注册模型、资源/能力中出现的具体模型名以及 `--model` 参数。
通配符覆盖无限集合，报告保留符号规则，不声称穷尽所有未来模型。对数据库大小写规则、CSV 空白和 `%` / `_`
的现有 SQL 行为，工具使用实际查询结果记录，不自动清洗或改变权限。

真实数据报告保存到项目内时，使用被 Git 忽略的 `artifacts/group-audit/` 目录；报告不含凭据，但仍属于内部路由数据，
导出前确认数据来源、目的地和授权。不要将报告提交到代码仓库。

本阶段已在无凭据列的 SQLite 样例快照上验证：授权矩阵、越组映射、诊断、重复输出一致、数据库文件不变、
写入被只读连接拒绝，以及不完整运行不输出基线。尚未清点生产数据，也未验证真实 MySQL 实例。
成员关系迁移前，应在实际数据快照上运行本命令，确认每一项越组映射的保留方式，再冻结报告用于比较新旧授权。
