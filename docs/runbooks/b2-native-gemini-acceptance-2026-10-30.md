# B2 原生 Gemini 工程验收

> 日期：2026-10-30（Asia/Shanghai）。范围：[B2 冻结契约](../design/b2-native-gemini-contract-2026-10-30.md)。状态：已部署生产，真实来源/SDK/付费验收保留。维护者确认没有指定 Gemini 客户端、模型或测试渠道；工程使用官方 REST 形状及隔离来源，生产完成鉴权、拒绝分支和健康验收。

## 1. 实现

- `/v1beta/models/{model}:generateContent` / `streamGenerateContent` 接入既有 staged executor、Identity/Channel 鉴权和模型映射、Billing 预扣/结算、Log canonical 用量。
- 文本、函数声明/调用/结果、JSON/SSE 和 thought signature 原生保留；图片/音频、服务端工具、隐藏缓存、原生服务档位、跨厂商转换、durable 试点及响应幂等缓存显式拒绝。
- 验证 Gemini 缓存子集与 candidates + thoughts 输出计量；缺少有效 usage 不估价收费。原生 canonical v1 不依赖旧 producer 开关。
- SSE 事件完整性、有界缓冲、终态/usage、真实零用量、取消和重复关闭均有验证。没有新增迁移、proto 或前端页面。
- RBAC 矩阵登记新入口和源码，权限仍为原有 API-key user/model/source/quota，不引入控制台 IAM operation。

## 2. 工程验证

| 验证 | 范围 / 状态 |
| --- | --- |
| 协议与相关回归 | `go test ./internal/gemini ./internal/adaptor ./internal/server/... ./platform/middleware ./domain/upstream/provider`；通过 |
| 三库 owner 链路 | `go test ./internal/integration -run TestGeminiNativeOwnerChain -count=1 -v`；临时本机 MySQL 8.4 / PostgreSQL 18 / SQLite，全量既有迁移后 HTTP → Identity/Channel gRPC → Billing/Log owner；三库各 5 例，共 15 例通过 |
| 完整门禁 | `make verify`；format / unit / race / architecture / migration / RBAC / IAM browser checker / generated API types / frontend lint、test、critical coverage、build 全通过 |
| 生产部署 | 2026-10-30 10:32 CST 更新 Relay，10:33 CST 完成 14 项公网检查、镜像/源码摘要和其他容器不变性核对 |
| 真实来源 | 未执行；未使用真实模型凭据，未产生供应商付费测试请求 |

原生报文测试覆盖：camel/snake 字段、重复键/别名、未知字段、媒体、工具往返及签名、缓存越界、负数/小数/total 矛盾、思考 token、CRLF、多行 data、SSE 断流/缺终态/缺 usage/错误/超限、零用量和 Billing 失败不重放。网关覆盖三种凭据、凭据不出站、模型 URL 映射、白名单、错误 envelope、共享中间件凭据隔离、durable allowlist 拒绝和结算关闭单次执行。

资金验收使用明确的测试价格，非生产报价：input=.001、output=.002、cache_read=.0002。供应商样本 prompt=10、cached=3、candidates=4、thoughts=2、total=16，实际 canonical=(7,3,0,0,6)。按现有 10000 金额刻度，消费为 196；重复提交不同 ActualTokens 仍返回第一次 196，只有一条 consume。缺 usage 和客户端取消释放 reservation，不产生 consume。账本与 Log canonical 字段分别核对。

最初完整门禁指出新增路由引起的 RBAC 行号及矩阵顺序未同步，已修正后重新通过；没有豁免门禁。三库验收不读取部署 `.env`，仅连接回环地址临时容器及测试临时文件；临时 schema 由测试清理，容器验收后停止删除。

最终源码摘要、门禁日志摘要、三库样本及外部保留项见[工程证据](evidence/b2-native-gemini-engineering-2026-10-30.json)。

## 3. 生产部署与回滚

2026-10-30 10:32 CST 在本机跨构建后更新生产 Relay；镜像为 `docker-compose-relay-gateway:b2-20261030-102901-f956fa5930d4`，源码摘要为 `f956fa5930d42e50ff5af289d28c300213860dde3742dc81fa241738f8a7d92f`。镜像与运行容器的源码 label、Linux/amd64 架构及 image ID 一致；`healthz` 通过，restart_count=0，启动日志 panic/fatal/error 均为 0。

仅更新 Relay 的 Compose image 和 source label；环境变量、私有 env 文件、安全设置和挂载保持一致，其余 20 个运行容器的 ID/镜像/重启次数未变。Billing/Log 复用既有契约，无新增数据库权限、迁移或数据库写操作；Web index 与 B1 部署摘要一致，没有发布前端制品。

正式 API 根地址为 `https://api.mengbin.top`。原有代理已透传 `/v1beta/`，没有修改反向代理。公网 14 项检查通过：JSON/SSE 缺密钥 401、无效 header/query Token 经 owner 鉴权返回 401、错误 JSON/媒体/服务端工具/响应缓存 400、不支持方法 501、冲突凭据 401、旧 Chat/Responses 鉴权 401、原生 CORS OPTIONS 204 及 healthz 200。控制台登录页仍使用原 Web index。以上请求均未进入真实供应商调用。

回滚备份为 `/opt/micro-one-api/backups/rollback-b2-20261030-102901-f956fa5930d4`，包含原 Compose/解析配置、私有 env、原容器 inspect、部署状态和旧镜像回滚 tag。若需回退，从备份提取旧 Relay 的 image/source label，仅恢复这两个字段后执行 `docker compose up -d --no-deps --no-build --pull never relay-gateway`，再查 healthz；不覆盖其他服务的后续配置。旧 Relay 会撤销原生入口，无需逆迁移，不改写历史账本。

详见[生产部署证据](evidence/b2-production-deployment-2026-10-30.json)。此前工程证据中的 `deployed=false` 是工程验收时点记录，生产状态以此节及生产证据为准。

## 4. 外部保留项

- [ ] 选定实际 Gemini 来源和模型，核对根 Base URL、凭据、模型映射、现有普通价格及输出/思考限额。
- [ ] 授权范围内完成真实 JSON/SSE、工具多轮与实际 usage / 账单验证。
- [ ] 如需实际 Google Gen AI SDK，登记语言、版本、base URL 和同一组能力样本；本轮没有安装或锁定 SDK 依赖。
- [x] 生产部署及线上鉴权/拒绝/健康验收完成，单独保存生产证据。

本次部署及文档纳入 `develop` 提交推送；不发布版本、不打 tag、不重开既有生产观察窗口，不启用其他阶段收费/支付或供应商监控。真实来源/SDK/付费验收继续保留。
