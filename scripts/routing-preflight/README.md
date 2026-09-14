# 分组 v2 只读预检

检查指定阶段的配置、所属 schema 迁移、回填、存量对象与实际服务能力。退出码：0 表示本次检查通过，1 表示前置不满足，2 表示参数 / 输入错误。输出 JSON 只含检查结果与汇总，不输出 DSN、服务令牌或底层驱动错误。

## 使用

在能访问数据库及服务私网的可信机器运行；源码工作区先按项目说明生成 proto。DSN 通过环境传入，分别指向 channel / identity / billing 所属数据库，不能用一个服务的迁移记录代替另一个。建议使用 SELECT 权限账号。

```bash
# 在环境中设置 ROUTING_CHANNEL_DSN、ROUTING_IDENTITY_DSN、ROUTING_BILLING_DSN、
# SERVICE_TOKEN、CHANNEL_GRPC_ENDPOINT、IDENTITY_GRPC_ENDPOINT、BILLING_GRPC_ENDPOINT。
docker compose --env-file deployments/docker-compose/.env \
  -f deployments/docker-compose/docker-compose.yml config --format json \
  | go run ./scripts/routing-preflight --driver=mysql --stage=f --user-id=1
```

`--stage` 支持 b / c / d / e / f。输入是 Compose 渲染 JSON，也可使用等价的 `services → 服务名 → environment` 对象（例如将 Kubernetes 实际环境投影为该结构）。配置输入可能含凭据，应直接管道传递，避免打印或上传原始文件。

检查配置的对象是输入中的目标配置；RPC 检查对象是指定 endpoint 的实际实例。预检不启动服务或修改开关。新环境先完成 schema / 回填与兼容服务升级，再按 runbook 逐阶段核验；开放创建入口前应通过对应阶段检查。负载均衡地址的一次成功不能证明所有副本都兼容，必须逐实例检查，并单独核对异步消费者版本。

## 数据库边界

- MySQL：`--driver=mysql`，每个 DSN 指向 owning schema。
- PostgreSQL：`--driver=postgres`，每个 DSN 明确数据库及 search_path。
- SQLite：`--driver=sqlite3`，三个 DSN 使用现存数据库文件的绝对路径。禁止内存库或附带任何 query / pragma 选项；工具强制 `mode=ro`。

工具使用只读事务中的 SELECT，直接读取已存在的 `schema_migrations`；不会初始化迁移表，不调用回填。identity 在阶段 C 启动就要求 outbox，因此 C 预检同时要求该 owner 的 095。

channel 回填记录与 identity 未映射用户是最低前置，不代替完整 `group-audit` 的授权矩阵和价格审计。能力检查使用现有 channel 列表、identity 用户事实及 billing capability RPC；没有独立 capability RPC 的行为仍由真实链路验收确认。

## 回退解释

`counts` 中报告 fixed / ordered Key、非空合约快照和未完成 v2 reservation。存在兼容性约束时写入 `warnings`；这些提示不使目标阶段的能力检查失败。关闭 admin 创建开关不删除存量对象；有合约或 v2 Key 时保留兼容读取，存在在途预扣时先排空，并保留兼容结算消费者。

生产步骤见[分组 runbook](../../docs/runbooks/routing-groups-runbook.md)，只读权限与异常回归位于同目录 `main_test.go`。
