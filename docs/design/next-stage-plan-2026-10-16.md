# 下一阶段计划：审查修复交付、定价原子保存与真实证据验收

> **当前执行入口**。制定：2026-10-16（Asia/Shanghai）。审查基线 `develop@a1913290`，起始工作区干净；审查修复已随 v0.35.1 正式发布，随后定价原子保存与撤权草稿修复已合入 develop 并上线。执行证据及未满足条件见 [本轮记录](../runbooks/plan-execution-2026-10-16.md)。接替 [10 月 12 日计划](next-stage-plan-2026-10-12.md)，保留已完成验收与条件项，不重复实施已交付工程。

## 本轮审查与修复

项目提供多服务 AI API 转发、用户授权、钱包结算和运营后台；按多用户、多进程共享数据库及 Redis 的部署场景审查。重点追踪鉴权、价格写入、额度暂停、上游契约和供应商证据链，不将本轮描述为逐文件审计。

| 优先级 | 触发与影响 | 已完成的最小修复 |
| --- | --- | --- |
| P1 | OAuth token/profile 的非成功 HTTP 响应或缺失身份仍返回成功；GitHub 缺失 ID 变成 `"0"`，可能形成共享错误身份 | 五个 provider 检查 HTTP 状态、必需凭据与身份；GitHub 邮箱接口错误不再产生已验证邮箱；保留正常登录和微信 token OpenID 回退 |
| P1 | 定价配置仍在读取、读取失败或缺读权限时可提交三个空价格 map；提前添加行还会固定空草稿，读取完成后丢失原价格 | 成功读回配置前禁止建立、编辑、删除和保存草稿；保存逻辑再次检查，读取失败显示错误 |
| P1 | Codex 只返回 secondary=96% 时，fallback 复制为 primary=96%，触发短周期 95% 阈值并误暂停账号 | 已识别窗口不再用于补另一个窗口；保留未标记窗口兼容，覆盖真实 HTTPServer 暂停接缝 |
| P2 | Gemini 版本在 `/v1beta/models/...` 路径中，冻结契约只读 header/query，发送前误拒绝 | 从实际模型路径识别版本，覆盖代理路径前缀；错误版本仍拒绝，观测与契约使用一致提取逻辑 |
| P2 | 供应商报告按最新导入优先返回，日报却持续覆盖 `LatestEvidenceID`，最终指向最旧证据 | 每个日报分组保留第一条最新证据 ID，经 `BillingUsecase.ListVendorReports` 验证 |

每个问题均先运行失败回归，再应用修复。另一个合成供应商请求 ID 冲突候选经调用链排除：现有 CSV 入口已拒绝保留前缀，没有为不可达问题增加代码。

可重复的回归入口（修复前均失败，当前均通过）：

| 命令 | 修复前信号 |
| --- | --- |
| `go test ./platform/security -run 'TestOAuthExchangeRejectsInvalidProviderResponses\|TestGitHubEmailRejectsErrorResponse' -count=1` | 20 个非法响应场景未返回错误；503 邮箱响应仍返回已验证邮箱 |
| `cd web && npm test -- src/pages/admin/PricingPage.test.tsx` | 未读成功的三组场景都实际写入三个空 map，`expected 3 to be +0`；提前添加草稿后找不到已有模型 |
| `go test ./internal/quota -run TestPartialCodexSnapshotKeepsWindowIdentity -count=1` | `window identity changed`，secondary-only 生成多余 primary |
| `go test ./domain/upstream/contract ./domain/upstream/evidence -run 'TestContractHTTPGeminiVersionMatchesURLPath\|TestAPIVersionWithGeminiProxyPaths' -count=1` | 合法路径返回 `API version does not match frozen contract`；代理前缀证据版本为 `unknown` |
| `go test ./app/billing/internal/biz -run TestVendorDailyRetainsNewestEvidenceID -count=1` | 预期 `newest-evidence`，实际 `oldest-evidence` |

## 审查时验证与边界（执行前）

- `make api` 刷新本地陈旧、被 Git 忽略的 protobuf 生成物，未手改生成代码。
- 标准单元范围 `go test $(go list ./... | rg -v '/test/e2e/suite$|/web/node_modules/|/internal/integration$')`，172 个包通过；另外 5 个 `internal/integration` 包通过。
- 前端 `npm test`：85 文件、536 项测试通过；完整 `npm run lint` 和 `npx tsc --noEmit -p tsconfig.app.json` 通过。
- 分层检查 `./scripts/check-architecture.sh`、RBAC 863 项入口契约检查、变更格式检查通过；quota/contract/evidence 的 race 测试通过。
- 未构建应用或镜像，未访问生产或发起真实供应商/OAuth 调用，未执行独立 MySQL/Postgres 实库矩阵或外部 E2E suite；依赖专用 DSN 的测试不能记作实库验收。

计划制定时最近的生产记录是 [管理页面更新记录](../runbooks/evidence-management-pages-deploy-2026-10-16.md)。上述未构建、未上线表述属于审查当轮；本轮实际部署见 [执行记录](../runbooks/plan-execution-2026-10-16.md)。executor 运行事实仍由 [观察手册](v0.23-executor-observation.md)及原始证据管理，不推断新的七天起点。

## 下一步顺序与退出条件

| 顺序 | 工作与责任范围 | 退出条件 |
| --- | --- | --- |
| 1：交付本轮修复 | 维护者/发布：先审查当前 diff，按仓库完整流程准备补丁发布；涉及 Identity、Relay、Billing 和独立前端 | 回归门禁与发布制品对应同一提交；本地跨构建 linux/amd64；前端独立发布；上线后分别验证 OAuth 失败拒绝、定价读取失败禁写、partial quota 不误暂停、Gemini 契约及最新日报证据。保留回滚制品，不改历史账本 |
| 2：定价原子保存 | Admin owner + Web：复用既有 `SystemOptionsBatchRepo.SetMany` 事务接缝，替换三个并行独立 PUT；无需新增 Config RPC | 任意一项校验/授权/CAS/存储失败时三项均不更新；两个管理员并发修改时只允许完整版本成功；返回值可读回确认，前端撤权/回读失败禁写。沿用现有分层，不用前端补偿覆盖历史值 |
| 3：历史误暂停核对及协议矩阵 | Channel/Relay：只读筛出 partial quota 触发的可能误暂停，结合当时窗口与真实额度人工确认；补 HTTP/SSE/WS 契约版本一致性 | 不自动解封真实耗尽/封禁账号；确认样本有原始 payload、窗口和原因；各协议包含带前缀 URL、错误版本、缺 usage、断流和旧 revision 拒绝回归 |
| 4：实库与独立供应商证据 | Billing/运营：在临时 MySQL/Postgres 环境执行关键并发/重放；选一个已有授权来源做完整账期 CSV/API 对照 | 并发重复结算一次生效，同 ID 不同内容拒绝，释放后迟到证据不追扣；账期、账户、币种、分页水位一致，差异有依据并可重放；没有外部金额的部分保持 unknown |
| 5：有限灰度准入 | 沿用现有 executor 观察流程，先核实当前实例、抓取连续性与同 endpoint/stream 的可比样本 | 满足现有起点条件才计连续七天；部署、重启或采集缺口按原规则重新确认，结论保持 PASS/FAIL/INSUFFICIENT；准入证据成立后再讨论扩面 |

## 本轮执行进展（2026-10-16）

- 第一项完成：`v0.35.1` 已按 develop → main → tag 推送，正式 Release 的 17 项任务及九服务双架构 manifest 独立核对通过。Identity、Billing、Relay 和独立前端已更新；七个相关进程环境摘要保留，健康正常、restart=0，outbox pending=0。
- 第二项完成并上线：使用 `PUT /api/option/` 的 `values` 三项完整价格 map 和 `expected_revisions` 字符串映射，沿用 owner 授权、事务和审计；草稿冻结版本并绑定授权版本，回读失败禁写、撤权不显示旧草稿。完整 `make verify`、三库 race 并发/回滚、最终 541 前端测试与 190 critical 通过；真实过期/不完整/未认证 API 返回 409/400/401，三个价格值与版本保持一致。后续工程提交未打新 tag，详见执行记录。
- 历史暂停只读核对：现存订阅账号为 kimi/minimax/zhipu 各一，Codex 为零；三条最新快照均没有暂停标记。本轮未恢复账号；快照覆盖写且没有原始历史 payload，不能据此证明已删除账号或历史时点均无误暂停。
- 实库：临时 MySQL 8.4/Postgres 16 的全量迁移、repeat/负例以及关键计费/证据回归通过。独立供应商账期仍待核验；当前浏览器对 DeepSeek URL 的访问被工具阻断，已停止该网站操作，不用已有未关账材料代替通过。
- 第三项协议工程完成：HTTP/SSE/WebSocket 共 12 项冻结版本、缺 usage、断流、隔离前拒绝回归通过；Gemini 路径前缀/版本不匹配及 owner 三库旧证明/回滚测试通过。
- 第五项条件复核为 INSUFFICIENT：14:42:05 CST 取证确认 Relay 自 10:48:16.088515 CST 启动，当前仅 legacy Responses SSE，缺可比 orchestrator cohort；不拼接旧窗口、不宣布正式七天起算。

当前只剩独立供应商完整账期与条件具备后的七天准入；收费名单、用户售价、权限发布和上游契约晋级仍按各自既有验收条件办理。本轮未自动恢复账号、导入供应商账单或扩大灰度范围。
