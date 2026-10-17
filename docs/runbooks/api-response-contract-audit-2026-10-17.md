# 前端接口响应格式排查与修复（2026-10-17）

接续[调用链前端响应契约故障](llm-request-timing-acceptance-2026-10-17.md)。按 `diagnosing-bugs` 流程先复现错误输入，再修复并验证。

## 范围与结论

核对前端 131 处显式 `apiClient/adminApiClient` 调用点，以及 IAM 动态请求、订单动态 client/path、OAuth 路径、status fetch、CSV/blob、Relay 模型目录/SSE。对照 admin/identity 的注册路由、对应 handler 与响应 writer；[调用点快照](evidence/api-response-callsites-2026-10-17.txt)是源码盘点，不代表逐个在线执行。

除上次已修复的三个调用链请求外，本轮未发现另一处“后端原始 JSON、前端按 data 包装拆包”的不匹配。发现并修复了两个相关的异常响应处理缺陷：公共解析器会静默返回 undefined；订阅购买会将缺失 data 当空对象并误报订单创建成功。后者由本地模拟异常响应稳定复现，没有证据表明线上购买曾触发。

| 接口族 | 实际成功响应格式 | 核对结果 |
| --- | --- | --- |
| 登录、注册、个人资料、Token、用户账单/支付、定价/status | OneAPI `success/message/data`；部分写操作无 data | payload 读取保留包装；注册及资料更新只校验成功状态 |
| 管理用户、渠道、兑换码、日志、选路审计、概览/经营分析、支付订单、对账 | 包装；部分写操作直接返回成功确认 | 列表/详情读取与 handler 一致，成功确认使用 `ensureApiSuccess` |
| 订阅套餐/额度策略/用户订阅/进度、路由分组/授权/计费策略 | 包装；部分操作无 data | 读取保留包装；发布计费策略仅校验成功状态 |
| 订阅购买/更换 | 包装，成功包含 purchase/change payload | 去掉缺失 data 的空对象回退；异常响应不再触发成功提示 |
| 模型目录、模型健康、模型导入导出、订阅账号、通知、路由运营、上游成本、供应商报告、上游契约、IAM、调用链 | 原始 JSON（部分包含自己的 success 字段，不等于 data 包装） | 直接读取 HTTP body；不套通用拆包 |
| CSV、Relay SSE、OpenAI 模型目录 | blob/流/协议特定 JSON | 保留专用处理，不按管理接口包装拆包 |

## 修改

- `unwrapApiData` 要求响应是对象且存在自身 data 字段，禁止把原始 JSON、HTML、null 或缺失/undefined data 静默交给查询/业务操作。显式 data:null、false、0、空字符串和空数组仍合法；服务端 success:false 的业务消息优先保留。
- `ensureApiSuccess` 拒绝非对象响应；继续支持合法的无 data 成功确认和空对象 RPC 确认。
- 注册、资料更新和路由计费策略发布使用成功确认校验，不再调用数据拆包函数。订阅购买/更换使用严格 payload 解析，不默认空对象；请求参数、权限、幂等键处理不变。

这次保留既有接口合同，避免用“有 data 就拆、没 data 就当原始对象”的通用容错掩盖下一次格式漂移。没有修改后端、账务、数据库或 IAM 权限。

## 回归

首次运行：

```sh
cd web
npm test -- --run src/lib/api-response.test.ts src/components/PurchasablePlansSection.test.tsx
```

修复前 7 失败/29 通过：五种不合法 payload 响应未按格式错误拒绝，两个购买场景错误提示成功。修复后相同反馈环通过；完整前端回归 **86 文件/560 测试通过**，lint、TypeScript、生产构建及资源预算通过。

线上只读验证：用户仪表盘加载；原始 JSON 模型列表有真实记录；包装格式路由分组列表和 default 分组详情有真实记录，结算策略正常显示。未进行生产注册、资料变更、权限操作、真实购买或支付。

## 部署与回滚

前端独立发布标识 `20261017-091657`，实际服务的 index SHA-256：`ec77f80ee0478d8433d9a57ab2a39b5ddbe2adc5dd144fe3a21834f0e088ddb2`。核对 HTML 与入口 assets 可读，保留 bind mount 原目录，未重启容器。备份：`/opt/web/dist.bak.api-response-20261017-091657`。

回滚时将该备份内容恢复到原 `/opt/web/dist` 目录内部，不替换目录 inode。此前的调用链修复包含在这个备份中。完整安全证据见[排查与部署数据](evidence/api-response-contract-audit-2026-10-17.json)。
