# A4：微信 Native 支付实施验收

实施与验收：2026-10-28（Asia/Shanghai）。关联[下一阶段排期](../design/next-stage-plan-2026-10-26.md)。

**状态：已部署（微信默认关闭），工程验收通过、待真实商户验收。** 2026-10-28 更新 Billing、Identity、Admin 与 Web，健康检查、真实管理员登录及渠道/订单读取、公网新页面、镜像与源码摘要核对通过；其他 18 个容器未重启。实际微信商户、付款与平台回调按维护者决定后补；未发布版本/tag。

## 1. 范围与资金边界

| 项目 | 实施边界 |
| --- | --- |
| 支付模式 | 一个单商户微信 Native provider；只支持 CNY 余额充值。微信订阅购买明确拒绝，原支付宝、钱包和订阅链路保留；Native 需要另一台设备扫码，不支持同机长按或相册识别 |
| 配置与密钥 | Billing 拥有商户、签名及回调配置；采用平台公钥 ID 模式。密钥文件只读挂载，配置和能力接口不向 Web 返回秘密 |
| 订单与结果不确定 | 充值请求携带幂等键，固定长度商户订单号；先持久化订单再请求平台。网络超时和无法验证的结果保留 pending；同键重试先查单，仅可信 NOTPAY/ORDER_NOT_EXIST 且未到期时，用原订单号与参数重入获取二维码，不把等待超时直接当成支付失败 |
| 成功校验 | provider 验证签名、时间戳、解密报文、AppID、商户号、Native 类型和 CNY；共享业务边界核对本地订单、渠道、金额、币种和平台交易号 |
| 二维码恢复并发 | 原单 Prepay 前以既有 `provider_payload` 做 30 秒条件认领，10 秒 HTTP 超时；写回要求同一认领凭证，租约替换后迟到响应不得覆盖新二维码。三库验证 8 路争抢仅一个持有者，并验证响应倒序；无需迁移 |
| 发放 | 查单与回调共用 `PaymentUsecase → MarkOrderPaid`；订单行锁、钱包增量、充值账本与幂等 claim 在同一事务完成 |
| 终态 | 只有可信平台终态可关闭本地订单；已支付单不被迟到关闭覆盖，已关闭单不因迟到成功通知再次发放 |
| BFF 与用户流程 | 正式回调代理保留原始 body 和 `Wechatpay-*` 签名头；渠道清单由 Billing 的实际配置派生。Web 本地展示二维码，继续支付复用该入口 |
| 不包含 | 平台原路退款、拒付、多商户实例、JSAPI/H5/APP 支付和新的订阅支付渠道 |

## 2. 已执行证据

| 验证 | 结果与边界 |
| --- | --- |
| 真实 SQLite 资金链路 | `TestWechatQueryAndNotifyIssueOnce/sqlite` 通过。使用全量真实迁移、真实 PaymentRepo/BillingUsecase/资产 issuer；16 路查单与通知并发，钱包只增加一次，充值账本只写一条 |
| 金额与乱序 | 同一真实 SQLite 测试覆盖查单和通知的错误金额、错误币种拒绝；已 paid 后的 close 不改变状态；重复成功通知不再次加钱；已 closed 后的成功通知拒绝 |
| 回调 HTTP 边界 | `TestWechatNotify*` 通过：原始报文字节与五个签名头保留、成功及重复回调返回 204、验签失败/非成功事件/未知订单/渠道或金额币种不符均不发放；非 POST、未配置和超过 64 KiB 输入在验签前拒绝 |
| Race 专项 | `go test -race ./app/billing/internal/data ./app/billing/internal/service -run 'TestWechat(QueryAndNotify\|Notify)' -count=1 -v` 通过，无 race 报告 |
| 真实 MySQL / PostgreSQL | 一次性 MySQL 8.4、PostgreSQL 16 本机容器；完整迁移及重复无操作验证后，`TestWechatQueryAndNotifyIssueOnce` 与既有 `TestConcurrentPaymentNotifyAndRefund` 的 SQLite/MySQL/PostgreSQL 六个子项全部通过（`-race -count=1`）。测试钱包发放及内部退款各只生效一次；容器已清理，凭据未落盘 |
| 协议签名/解密专项 | 官方 SDK `v0.2.21`；`go test -race ./app/billing/internal/data -run '^TestWechat' -count=1` 通过。RSA 请求签名/应答验签、AEAD/时间戳、错误商户/币种/金额、未知状态、超期关单竞争、可信与未签名 ORDER_NOT_EXIST、body 限额、停新单仍查单/接收回调均覆盖 |
| Admin / Identity / Web | 相关 Go 包与 `go vet` 通过；Web 专项 21 例及桌面/移动 Chrome 2/2 通过，含官方 `/bizpayurl/up` 二维码、无二维码订单复用幂等键、终态后同金额新充值、支付后余额刷新、缺订单号保护、继续支付/刷新状态、禁用渠道和订单筛选；实际检查截图无溢出。浏览器 API 使用本机 fixture，真实跨服务联通另行登记 |
| 真实 HTTP / gRPC 支付联通 | `TestIAMWechatPaymentThroughRealOwners` 通过 `-race`：真实 Identity 登录会话 → Admin BFF → Identity HTTP → Billing owner gRPC → 真实 Native SDK → 本机 TLS 签名 fixture；BFF 回调 → Billing RSA 验签/AEAD 解密 → 真实 SQLite 钱包和账本。渠道无会话及直接 owner 缺用户凭据拒绝；下单幂等、订单列表、错误签名零发放、成功和重复通知单次发放、已支付订单读回全部通过。原支付宝订阅购买/钱包续期 `TestIAMSubscriptionPaymentThroughRealOwners` 同组 `-race` 通过 |
| 生成契约及完整门禁 | `make all`、最终 `make verify` 全通过：format、Go unit/race、architecture、migration、RBAC 915 行、IAM browser gate、生成类型无漂移、eslint、Web 95 文件/657 例、关键覆盖 16 文件/194 例、TypeScript/build/预算全部通过 |
| 机器评审与最终自审 | 完整高强度 OCR 已完成并逐项处理：真实问题已修，Number 解码与 204 通知两项按测试/官方协议驳回；最终增量外部调用超时，并在 502 排查时停止，**不将其计为通过**。关键资金增量改用 `ocr delegate` 规则进行独立宿主复核，专项 race 通过，无阻塞 P0/P1/P2，见 [评审处置](evidence/a4-review-2026-10-28.json) |
| 生产部署 | 三张 linux/amd64 镜像 ID、源码摘要、健康检查、启动无 ERROR、restart=0、完整运行环境哈希不变；公网渠道未登录 401、微信关闭态回调 503、支付宝非法回调拒绝、新 Web index 一致。真实管理员登录、渠道仅支付宝、订单读取及退出均通过，未创建支付单，见 [线上证据](evidence/a4-production-deployment-2026-10-28.json) |

支付跨服务机器可读证据保存在 [`evidence/a4-payment-owner-chain-2026-10-28.json`](evidence/a4-payment-owner-chain-2026-10-28.json)，脱敏输出为本机 `/tmp/a4-payment-owner-race.log`。

额外既有测试限制：扩大回归时 `TestIAMSelfUsageThroughDedicatedIdentityCaller` 在改写全局 `time.Local` 的时区子项检测到 data race（`internal/integration/iam_self_usage_test.go:123` 与 gRPC 后台 `time.Now` 竞争）。已在未含 A4 改动的原始 `develop@745096ab` 独立重跑同测试复现；该文件无改动，本项不修复任务外测试。基线输出保存在本机 `/tmp/a4-self-usage-baseline-race.log`。`make verify` 不包含此 integration，完整门禁通过与这个额外 race 分别登记；微信和原支付宝支付两 suite 独立 `-race` 全绿。

三库机器可读证据保存在 [`evidence/a4-payment-three-database-2026-10-28.json`](evidence/a4-payment-three-database-2026-10-28.json)。完整脱敏测试输出为本机 `/tmp/a4-three-db-test.log`，不含连接凭据。

专项复现：

```sh
go test ./app/billing/internal/data ./app/billing/internal/service \
  -run 'TestWechat(QueryAndNotify|Notify)' -count=1 -v
go test -race ./app/billing/internal/data ./app/billing/internal/service \
  -run 'TestWechat(QueryAndNotify|Notify)' -count=1 -v
go test -race ./app/billing/internal/data \
  -run 'Test(WechatQueryAndNotifyIssueOnce|ConcurrentPaymentNotifyAndRefund)$' -count=1 -v
go test -race ./internal/integration \
  -run '^TestIAM(WechatPaymentThroughRealOwners|SubscriptionPaymentThroughRealOwners)$' -count=1 -v
```

三库专项复用 `platform/database/testutil.RoutingContextDB`。SQLite 始终执行；设置 `GROUP_CONTEXT_TEST_MYSQL_DSN` / `GROUP_CONTEXT_TEST_POSTGRES_DSN` 后执行另外两库。仅允许本机一次性数据库，各子测试自动创建、完整迁移、重复验证迁移无操作，并删除隔离 schema；不得传入生产 DSN。

## 3. 配置与候选升级

- `WECHAT_ENABLED=false` 为默认值，只停新单和渠道展示；已配置商户号时仍初始化完整密钥以处理存量查单/回调，排空前不删除商户材料。启用时需要 `WECHAT_APP_ID`、`WECHAT_MCH_ID`、`WECHAT_CERTIFICATE_SERIAL`、`WECHAT_PRIVATE_KEY_PATH`、`WECHAT_PUBLIC_KEY_ID`、`WECHAT_PUBLIC_KEY_PATH`、`WECHAT_API_V3_KEY`、`WECHAT_NOTIFY_URL`；通过既有秘密注入方式提供，不写入版本库、证据或日志。
- `WECHAT_CERT_DIR` 只读挂载到 Billing 的 `/cert/wechat`；公钥 ID 使用平台公钥模式，商户私钥与平台公钥分别配置，不将同一把测试密钥当成真实商户材料。
- 正式入口为 `POST /api/v1/user/payments/wechat/notify`，兼容路由为 `POST /api/user/payments/wechat/notify`。回调地址必须为可公网访问的 HTTPS 地址；反向代理不得改写签名 body。此回调使用 provider 身份验签，不要求用户会话或控制台 step-up。
- 候选受影响服务为 Billing、Identity、Admin 和 Web；Web 仍通过宿主机 `web/dist` 独立发布。生产镜像按既有流程在本机/CI 交叉构建，禁止在生产主机构建。
- 本项复用现有支付订单和充值账本表，无新增数据库迁移。本次已获部署授权，部署先升级 Billing 再升级 Identity/Admin/Web，真实验收前保持关闭。A4 本身无迁移；因生产尚未包含 A3，新 Billing 需先补既有 144 号兼容字段迁移。Log/Relay 不随本次重建，档位计价保持关闭。
- 回滚前关闭新充值入口并排空或交接 pending 订单、回调和查单任务；不能让不支持微信的旧 Billing 接管未完成微信订单。保留历史订单、钱包和账本，不通过删单或重算历史金额回滚。

## 4. 外部验收保留项

- [ ] 真实商户资质、Native 产品权限、绑定 AppID、商户证书序列号、平台公钥及密钥准备完成；确认实际收款方。
- [ ] 真实可达回调地址、TLS、代理报文保持和时钟同步验证。
- [ ] 明确授权小额真实支付后，核对扫码页面、实际付款、通知、主动查单、钱包和账本；实际收款与工程 fixture 分别留证。
- [ ] 平台通知重试、查询与回调并发、超时但成功、到期关单与付款竞争，在可用平台条件下独立验收。
- [ ] 原支付宝真实支付能力回归；钱包/订阅工程回归与真实平台付款分别登记。
- [x] 生产部署与关闭态线上验收：2026-10-28 已更新 Billing/Identity/Admin/Web；回滚点 `rollback-a4-20261028-202555`，Billing 备份、旧 Compose/Web/镜像保留。
- [ ] 微信真实收费启用及发布版本流程；本次未启用、未发版/tag。

协议依据：[官方 Go SDK](https://github.com/wechatpay-apiv3/wechatpay-go/tree/v0.2.21)，包括 Native API、公钥 ID 验签和 notify 解密。第三方 SDK 的 JSON 实现为外部依赖；本仓业务代码仍统一使用 `pkg/jsonx`。

官方协议核对：[Native 下单](https://pay.wechatpay.cn/doc/v3/merchant/4012791877)、[Native FAQ](https://pay.wechatpay.cn/doc/v3/merchant/4012791890)、[Native 通知](https://pay.wechatpay.cn/doc/v3/merchant/4012791882)。通知允许 200/204 且无需 body，本实现返回 204；未支付响应允许缺少金额和交易类型，成功入账仍要求全部资金字段。

最终工程门禁汇总见 [机器可读记录](evidence/a4-engineering-acceptance-2026-10-28.json)。本次部署补既有 Billing 144 号迁移，Log 145 号未应用、Relay/Log 未重建，A3 仍不是完整计价上线。公网入口按 Identity 的实际公开控制台配置核对；镜像与 Compose 的既有源码标签已同步，运行凭据及策略环境哈希不变。

部署前收到既有 Relay 502 告警：当时核心容器健康且尚未切换 A4；发现错误率表达式保留 status 标签导致 502 序列自除为 100%。本次停止通过线上网关发起的 OCR 请求，告警规则候选仅在临时目录验证，按维护者指令继续 A4，**未修改告警规则**。该告警问题与真实上游失败分别处理，不把“100%”解读为服务全停。
