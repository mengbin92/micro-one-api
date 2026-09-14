# 分组 v2 真实链路验收

使用仓库真实服务二进制、数据库和 Redis，通过 admin HTTP 与内部 RPC 建立测试数据，经 relay 调用本地 mock 上游并核对账务。mock 只替代付费上游；mock 支付使用 billing 自带的本地 provider。两个 relay 实例验证撤权广播。

## 运行

```bash
make test-routing-e2e
make test-routing-e2e ROUTING_E2E_DRIVER=sqlite3

# 复用刚构建的同一版本镜像，避免重复编译
python3 scripts/test-routing-e2e.py --driver=sqlite3 --skip-build
```

要求 Docker Compose、Python 3 和可下载构建依赖的网络。脚本从已跟踪及未忽略的新文件建立源码快照；不读取生产 .env。每次生成独立项目名、数据库卷、测试凭据和网络，不映射服务端口到宿主。完成或失败后默认只清理本次项目；`--keep` 可保留诊断环境，并打印精确清理命令。构建在本机完成。

日志目录由脚本打印。`acceptance.log` 保存用例结果，`preflight.json` 保存阶段 F 预检，`build.log` 保存构建结果；环境与夹具文件包含随机测试凭据，仅保存在私有临时目录 / 测试卷。CI 仅上传前三种日志，不上传原始环境、Compose 配置和服务日志。

共享 [E2E workflow](../../../.github/workflows/e2e.yml) 增加 MySQL / SQLite 矩阵，nightly 与 release 复用该入口。普通 `go test` 跳过此包的服务验收；`make verify` 不包含完整 E2E。

## 场景与断言

| 场景 | 验证内容 |
| --- | --- |
| legacy → v2 | 默认关闭开关时 inherit 聊天、旧合同购买与结算；运行实际审计、两项回填工具后启用，原 Key 进入 v2 |
| fixed | 新建停用组、加资源、发布价格、启用、授权、建 Key、models、SSE 请求与冻结组账单 |
| 撤权与事件 | 两个 relay 先缓存有效请求；撤权、停组、重复 / 乱序事件不恢复访问；拒绝时无上游调用与钱包扣款 |
| 冻结计价 | 预扣后将用户倍率 2 改为 3，在途使用 2，下个请求使用 3；HTTP 幂等重试不重复扣款 |
| ordered | 无资源候选递进；无资格、结算失败、候选耗尽和能力缺失均拒绝，不回退全局组 |
| subscription_only | 文本 embeddings 在上界内仅扣订阅；额度不足 / 无覆盖拒绝，聊天因无费用上界拒绝 |
| subscription_first | 部分订阅额度 + 钱包拆分，费用守恒；到期立即失去订阅来源资格，独立授权下允许钱包回退 |
| 合同 | 购买幂等、续期保留用量、变更覆盖、撤销、mock 支付履约及退款撤权；独立 admin 授权按来源保留 |
| 会话 | fixed / ordered 的 Responses、SSE、WS 多轮始终结算原组；撤权 / 停组拒绝后续调用 |
| 故障与回退 | Redis 停止后复用会话执行撤权、outbox 保留，恢复后投递；确认 billing 可达且能力关闭后拒绝请求；关闭 admin 创建开关后已有 Key 仍可用 |

额度、账单与对象断言查询测试库。唯一直接修改业务数据的时间夹具用于将订阅过期时间移到过去，以验证请求时的期限检查；业务创建、授权、购买、变更和撤销均走真实接口。账务请求 ID 由 relay 自己生成，测试按顺序将新 reservation 与场景关联，不能把客户端 X-Request-ID 当作账务 ID。

## 覆盖边界

此入口验收 MySQL 与 SQLite 的完整服务接线；PostgreSQL 继续运行现有数据边界和迁移测试，未加入完整服务 E2E。Kubernetes 本次只验证 manifest / 引用。测试不代替生产小额样本、告警窗口、外部支付签名链路或新订阅协议的费用上界证明。

运维指标与告警属于 [v0.30 P1](../../../docs/design/v0.30-roadmap.md)，此处只验证持久化、投递和权限不变式。
