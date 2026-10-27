# A2：TOTP、恢复码与 step-up 实施验收

2026-10-27（北京时间）。关联 [排期](../design/next-stage-plan-2026-10-26.md) / [首批入口契约](../design/a2-security-contract-2026-10-27.md)。

**状态：工程验收通过、待外部验证。** 本次完成代码、迁移、契约、页面和本地验收；未部署生产、未 push、未发布版本/tag。实际投入按验证证据登记，不将原排期的 11–16 工程日视作已发生工时。

## 1. 交付范围

| 项目 | 实现与证据 |
| --- | --- |
| TOTP 登录 | 密码和 OAuth 使用同一末端状态机；启用 MFA 后首步只返回专用 audience/type 的五分钟 challenge，不能作为 session 使用 |
| 恢复与设置 | Profile 安全中心：手动密钥/验证器链接、验证启用、一次展示十个恢复码、换码、禁用；恢复码可完成 MFA 登录和 step-up，不提供邮件绕过 |
| OAuth-only | 无密码账号完成新的 OAuth 登录后，对新 JTI 发五分钟初始证明，可设置 MFA；过期后需重新 OAuth 登录。启用后必须经过 TOTP/恢复码 |
| OAuth 浏览器交接 | 无 MFA 的正常 callback 通过一分钟 HttpOnly/SameSite cookie 向 SPA 交接，完整 session 不进入重定向 URL；有 MFA 时只重定向短期 challenge |
| 秘密存储 | Secret/待确认 Secret 采用独立密钥 AES-GCM；恢复码为 128-bit 随机值，只持久化 SHA-256；返回一次，不写浏览器持久存储或审计 |
| 重放与限次 | 既有 IAM policy 锁串行消费 SQL 时间步/恢复码；A1 Redis challenge 单次消费。全账号五次失败锁定五分钟，换实例/换 challenge 不重置 |
| 短期证明 | Redis 保存现有 JTI 的证明，绑定用户、密码 epoch、MFA revision，五分钟到期；IAM 模式重新核对会话撤销。修改密码/MFA 后旧证明失效 |
| 首批执行点 | MFA 管理、用户密码/邮箱、API Key 新建、渠道秘密/导出、兑换码读取/列表/导出、IAM 实际管理写；详见冻结入口表。保留原 CAS/scope/权限检查 |
| 事务与审计 | MFA 变化推进账号 epoch 并撤销旧会话；self 敏感更新和 Token 新建在事务内再检查证明。失败尝试以 `*.failed_attempt` 成功记录限次变化，`diff.verified=false`，不误记为认证成功 |
| 兼容 | 未启用 MFA 的旧用户继续原登录；显式 memory 开发模式不提供 MFA，持久化依赖失败不会回退至 memory。没有新增依赖 |

恢复码对话框位于路由/语言切换之外，在用户明确保存前保留内存中的一次性展示；刷新页面不会重新读取恢复码。全部恢复码和设备同时丢失时，不支持自助解除 MFA。既有 root rescue 仅重置密码，**不能解除 MFA**；线下恢复需另行核验和授权。

## 2. 验证结果

| 状态 | 验证 | 结果 |
| --- | --- | --- |
| ✅ | `go build ./...` / `go vet ./...` | 通过；使用仓库要求的 Go 1.27 工具链（本机 1.27.3） |
| ✅ | `make verify` | format、unit、仓库 race、architecture、migration、906 条 RBAC 入口、IAM browser 结果检查、生成类型、Web lint/620 项测试/191 项 critical 测试及覆盖率/build 全部通过 |
| ✅ | 认证专项 `go test -race` | Identity data/cmd 的 Security/OAuth-only 用例通过；补足仓库 race 默认未包含的 Identity 包 |
| ✅ | 三库真实迁移及状态机 | 隔离 MySQL 8.4、PostgreSQL 16、SQLite；运行完整迁移树、重复迁移检查及新增 MFA 状态，无生产 DSN |
| ✅ | 双实例 SQL/Redis | 独立 owner SQL/Redis 客户端；实例 A 设置/发 challenge，实例 B 验证；同一 TOTP 时间步、同一 challenge/恢复码并发仅成功一次 |
| ✅ | 失效与拒绝 | proof TTL、其他 JTI、会话撤销、MFA 变更 epoch、旧恢复码、错误加密密钥、依赖不可用、超大/未知请求字段与限次拒绝 |
| ✅ | BFF / owner HTTP / gRPC | 真实 Admin BFF → Identity；直接 Token 创建拒绝/允许；Channel/Billing 专用 owner RPC 导出在无 proof 时拒绝，有 proof 后通过 |
| ✅ | OAuth HTTP 链路 | 隔离 provider 替身验证真实 callback 状态检查、cookie 交接、MFA challenge 及恢复码完成；无外部账号或真实授权凭据 |
| ✅ | 真实桌面/移动 Chrome | 实际构建产物、BFF、Identity HTTP/gRPC、SQL/Redis；设置→保存恢复码→错误因子拒绝→MFA 登录→step-up→新 API Token→换码/旧码拒绝→禁用；两端通过，页面无横向溢出 |
| ✅ | Web 自动化 | 新增首步不存 token、安全中心验证/一次展示、二次认证后重试且不无限重试；旧凭据的 401/403 不触发换会话后的 step-up 重试或清理新会话；旧 401 不打断恢复码展示；原登录/资料页及完整前端套件通过 |
| ⚠️ | 真实外部 OAuth / 手机验证器 | 尚未使用真实外部 OAuth 账号、实际手机 App 验证；本地 callback 替身和 RFC 向量不替代这些条件 |
| ⚠️ | 生产部署/开关启用 | 未执行；生产数据、既有监控停用、契约灰度和自动任务均未修改 |

复现命令（外部数据库环境变量必须指向**本机一次性数据库**，不要使用部署 DSN）：

```sh
go build ./...
make verify
go test -race ./app/identity/internal/data ./app/identity/cmd/identity \
  -run 'TestSecurity|TestOAuthOnly' -count=1
# 设置 GROUP_CONTEXT_TEST_MYSQL_DSN / GROUP_CONTEXT_TEST_POSTGRES_DSN 后执行三库用例：
go test ./app/identity/internal/data -run 'TestSecurity|TestOAuthOnly' -count=1 -v
# 先构建 Web，再启动真实 BFF/owner 与 Chrome（测试自动管理隔离 SQLite/Redis）：
(cd web && npm run build)
A2_PLAYWRIGHT=1 go test ./app/identity/cmd/identity \
  -run TestSecurityOwnerIntegration -count=1 -timeout 3m
```

`make verify` 的生成类型 gate 比较 Git index；提交候选前应将有意更新的 `web/src/types/api.ts` 纳入候选，再检查重新生成没有漂移。浏览器认证测试关闭 trace/video/失败 screenshot；仅保存设置前无秘密的安全中心截图供桌面/移动布局核对。

## 3. 配置与发布顺序

| 配置 | 默认 | 行为 |
| --- | --- | --- |
| `IDENTITY_MFA_ENCRYPTION_KEY` | 空 | 16/24/32 字节独立秘密；空值不允许 enrollment，错误长度启动失败；所有 Identity 副本一致，需安全备份 |
| `IDENTITY_STEP_UP_ENABLED` | `false` | 控制首批敏感操作强制证明；MFA 自身管理始终要求新证明，MFA 登录始终执行 |
| `IDENTITY_MFA_REQUIRE_ADMIN` | `false` | 进一步要求首批高权限操作先绑定 MFA；启用时必须配置加密密钥，不能用密码 proof 绕过 |

两个开关仅接受 `true` / `false`，不将大小写或误填值静默解释为关闭。环境模板已覆盖普通、Lite、PostgreSQL 部署；主 Compose 已注入 Identity。

1. 备份目标 SQL 和 Redis，使用带北京时间时间戳的备份目录；安全保存对应加密密钥引用，**不要把密钥写进文档**。
2. 应用 `143_create_identity_mfa.sql`：MySQL 主目录、PostgreSQL/SQLite 对应镜像目录。该迁移仅新增 Identity-owned 表，不重算资金，不自动发布 IAM 权限。
3. 配置统一独立加密密钥；部署 Identity、Admin BFF 和 Web。镜像仍须本机构建 Linux/amd64，Web 仍单独更新宿主机 dist。两个强制开关先保持关闭。
4. 用指定测试账号/实际验证器完成 enrollment、恢复码离线保存、密码/OAuth 登录和敏感操作验证；未通过时不启用管理员强制策略。
5. 逐步启用 step-up，再按维护者决定启用管理员 MFA。重新创建时只操作 Identity，不重建无关服务；复查 BFF 和直接 owner 拒绝分支。

这是新增认证能力，后续若独立发版应判为 **MINOR**；本次不预定版本号，实际发布仍执行完整七步流程。

## 4. 回滚与保留项

- 已有用户启用 MFA 后，不能回滚到忽略 MFA 的旧 Identity；也不能丢弃表或加密密钥。允许关闭两个强制策略开关，但必须保留 MFA 登录校验及数据。
- SQL 事务和 Redis challenge 跨存储失败采用 fail-closed：可能消费 challenge 后 SQL 未提交，用户需重新发起首步；不会签发第二个成功会话。该可用性边界不扩大为分布式事务系统。
- 首批之外的 owner 写操作、Passkey、支付回调、worker/system 专用凭据、全部因子丢失后的线下恢复均保留既有边界；不能据本项声称“所有管理操作均完成 step-up”。
- [ ] 真实外部 OAuth 与实际验证器验收。
- [ ] 生产备份、迁移、部署、分阶段策略启用及线上验证。
