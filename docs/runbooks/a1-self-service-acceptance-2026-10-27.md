# A1 注册、邮箱验证与密码恢复实施记录

状态：**已部署；Turnstile 未启用，启用时另验**。日期：2026-10-27（Asia/Shanghai）。实施基线：`develop@14293365`。对应[当前排期 A1](../design/next-stage-plan-2026-10-26.md#a1注册与恢复的完整调用链)。本次交付 Identity、Admin BFF、Web 和配置模板；2026-10-27 07:28 CST 已部署并通过生产验收，尚未发布新版本/tag。

## 实现与接口

| 范围 | 已实现行为 |
| --- | --- |
| 公开策略 | Identity `GET /api/registration/policy` 为注册开关、邮箱验证、域名和 Turnstile 的事实源；BFF `GET /api/status` 只合并安全字段。Owner 不可达时禁用注册，品牌信息仍可读取；不公开 SMTP 凭据或 Turnstile secret |
| 注册页面 | 根据公开策略展示邮箱、域名说明、验证码和 Turnstile；支持邀请码。邀请码注册要求邮箱所有权证明；邮件不可用时提示移除邀请码后按普通注册策略继续 |
| 邮件链路 | BFF 代理 `GET /api/verification?email=...`、`GET /api/reset_password?email=...` 和 `POST /api/user/reset`。有副作用的公开接口与策略使用 `Cache-Control: no-store` |
| 共享 challenge | 复用 Identity 的 Redis 客户端，按用途和规范化邮箱隔离，保存随机码的 SHA-256，不保存明文码；10 分钟 TTL、60 秒重发间隔、最多 5 次错误、原子单次消费。消费后保留重发冷却；新发码替换旧码 |
| 发送失败 | 先写 pending，SMTP 接受后条件激活；失败条件撤销本次记录，可重试。旧请求不能激活或删除后来发出的码；Redis 或 SMTP 未配置时失败关闭，无进程内回退或吞码后报成功 |
| 恢复隐私 | 公开请求在查询账号、连接 SMTP 前统一有界异步接受；每个 Identity 实例最多 4 个任务、无队列、独立 20 秒上下文。未知邮箱、不可恢复账号、发送失败及冷却均不公开账号事实；响应只表示请求已接受。容量不足统一返回服务不可用 |
| 密码重置 | 新增 `/forgot-password` 和 `/reset-password`，支持中英文。邮件链接把 token 放 fragment，页面读取后移除 URL 秘密；重置成功清除浏览器会话。Owner 继续校验邮箱绑定、用户 ID 和密码版本，更新密码后旧会话失效，重置 token 不可重放 |
| 邮箱绑定 | 在既有 IAM policy 写锁下检查新邮箱绑定，大小写/首尾空白不能创建重复占用；空邮箱仍允许多个账号。历史重复地址不自动改写，恢复查询遇到歧义时拒绝选择任意账号；唯一旧绑定可跨大小写恢复 |
| 奖励 | 注册、邀请人和受邀人奖励分别传稳定 `request_id`；复用 Billing 的账本去重。只有注册创建成功才调用奖励，邀请奖励仍要求已消费邮箱证明。内存开发模式也在锁内检查用户名和邮箱冲突 |

验证码为 128 bit 随机值，通过邮件复制使用；不在 HTTP 响应或日志中返回。既有公开 IP/邮箱限流继续生效，重发冷却并不替代该限流。

## 邮件与注册配置

线上已有的 `NOTIFY_SMTP_HOST / PORT / USER / PASS / FROM` 可以整组复用，Compose 三种模板已将它们注入 Identity。无需重复配置邮箱账号。

必须另设控制台的浏览器访问地址，例如：

```dotenv
IDENTITY_PUBLIC_BASE_URL=https://console.example.com
REGISTRATION_DISABLED=false
REGISTRATION_EMAIL_VERIFICATION_ENABLED=true
REGISTRATION_EMAIL_DOMAIN_RESTRICTION_ENABLED=false
REGISTRATION_EMAIL_DOMAIN_WHITELIST=[]
TURNSTILE_CHECK_ENABLED=false
TURNSTILE_SITE_KEY=
TURNSTILE_SECRET=
```

`IDENTITY_PUBLIC_BASE_URL` 不从请求 Host 推断；要求 http/https、无用户名、query 或 fragment。它用于生成重置页面链接，未配置时邮件功能明确不可用。示例地址必须替换为实际控制台地址。

- 如需独立身份邮件账号，配置 `IDENTITY_SMTP_HOST / PORT / USER / PASS / FROM / MODE`。一旦设置 Identity HOST，即使用完整 Identity 组，避免混入 Notify 的另一套凭据。
- MODE 留空时，465 自动采用隐式 TLS，其余端口采用 STARTTLS；生产验证证书且禁止降级。`plain` 仅允许本机回环 SMTP 测试收件器。
- Turnstile 开启时同时配置 site key 和 secret；浏览器只收到 site key。未配置完整时注册失败关闭，默认关闭保持兼容。
- Kubernetes 可通过可选 `identity-registration` ConfigMap 注入开关/公网 URL，以及 `identity-email` Secret 注入上述 SMTP 环境变量。模板不包含真实密钥。

配置源见 [Identity config](../../app/identity/configs/config.yaml)、[环境变量示例](../../.env.example)、[Compose](../../deployments/docker-compose/docker-compose.yml)和 [Kubernetes](../../deployments/k8s/identity-service.yaml)。

## 验证记录

最终证据摘要见 [A1 验收摘要](evidence/a1-self-service-2026-10-27.json)。本地测试使用临时数据库、Redis 和 SMTP 收件器，未在生产注册账号、重置密码或制造故障。

| 检查 | 结果 / 范围 |
| --- | --- |
| `make api` / `make config` / `make wire` | 通过；领域错误 reason 枚举、配置与 Wire 按生成流程刷新，未手改生成代码 |
| `make verify` | 通过：格式、Go 单元/race、分层、迁移、RBAC、IAM 浏览器结果检查器、API 类型、前端 lint；91 个文件 / 616 项前端测试，关键路径 191 项及覆盖率门禁、构建和资源预算通过 |
| 共享 Redis 与 SMTP | 通过：双独立客户端、并发仅一次消费、TTL、5 次错误、重发替换、旧请求 CAS、失败撤销、发送后冷却；真实临时 Redis 及本机 TCP SMTP 收件、451 失败、TLS 禁降级分别验证 |
| 真实三库 | 通过：SQLite、MySQL 8.4、PostgreSQL 16 的邮箱并发绑定、历史重复恢复拒绝、legacy/IAM 跨大小写恢复及旧会话撤销。相关 race 回归通过；临时数据库容器已清理 |
| BFF 与双实例链路 | 通过：两套真实 BFF → 两个 Identity/独立 SQL 与 Redis 客户端 → SMTP；完整 SQLite 迁移，legacy/IAM 两种模式。实际 Redis 7 与默认 miniredis 的 race 验收均通过；覆盖跨实例注册/恢复、奖励仅一次、旧会话失效和 451 失败后重试 |
| 浏览器 | Chrome 156 的中英、桌面/移动专项 16/16 通过，0 跳过/flaky/JS 错误/外部请求；实际切换语言保留已读 token 与草稿，完成/离开清秘密，旧恢复响应不能清掉新流程或后来建立的会话。24 张截图与本机报告保存在被忽略的 `web/a1-test-results/`，可用下方命令复跑 |
| 真实 SMTP | 使用线上现有 Notify SMTP 配置，通过本次新的 Identity 投递实现，向用户指定测试邮箱提交 1 封验证邮件；2026-10-27 05:10:31 CST 获 SMTP 接受。凭据只在进程内传递，未落入源码/证据；2026-10-27 收件人回复“已收到”，真实邮件收件验收通过 |
| 真实 Turnstile | 组件生命周期、过期/错误、提交/重试与服务端拒绝分支通过；真实 Cloudflare 配置和浏览器挑战待启用环境验证，未将 mock 当作外部验收 |

链路验收中的奖励使用记录型 Billing 客户端，核对调用单次发放与非空且互不冲突的幂等键；账本去重由既有 Billing 测试覆盖，本次没有真实充值或发放生产资产。

可复跑入口：

```sh
make verify
go test -race ./app/identity/internal/data -run 'TestChallenge|TestSMTP|TestEmailBinding|TestHistoricalDuplicateEmail|TestCaseInsensitiveEmail|TestMemoryEmailBindings' -count=1
go test -race ./app/identity/internal/biz -run TestRecovery -count=1
go test -race ./app/identity/cmd/identity -run '^TestEmailAuthenticationOwnerIntegration$' -count=1
cd web && npm run test:e2e:a1
```

真实 MySQL/PostgreSQL 使用仓库测试工具的 `GROUP_CONTEXT_TEST_MYSQL_DSN` / `GROUP_CONTEXT_TEST_POSTGRES_DSN` 指向隔离数据库；未提供时对应测试会跳过，不能据默认命令宣称三库完成。真实 Redis 使用 `IDENTITY_CHALLENGE_REDIS_TEST_ADDR` 指向专用临时实例，不可使用生产 Redis。

## 生产部署（2026-10-27 07:28 CST）

按维护者要求先部署、再提交 `develop`。部署清单为 `a1-20261027-072232-4cae2d8e1ff2`，源码摘要为 `4cae2d8e1ff2ac7e3fe6727ca47af031f6e3ef3d8bb26548310f42f3ab06f046`；两镜像均在本机交叉构建为 Linux/amd64，生产仅加载镜像，未构建镜像、未执行 SQL 迁移。详见[生产证据](evidence/a1-production-deployment-2026-10-27.json)。

| 交付物 | 生产核验 |
| --- | --- |
| Identity | 镜像 `sha256:984fb56240458eadd40fa7ce285c119a2b9daa10158b68c610e9b83f138a6569`，健康通过，restart_count=0 |
| Admin API | 镜像 `sha256:8022c2606e0caee6c5860d3a82a04bfdd863819f42c4085c5b01510715778f79`，健康通过，restart_count=0 |
| Web | 宿主机 `/opt/web/dist` 独立发布；公网登录页与本机构建的 index 摘要一致。新 index 最后原子替换，旧 hash 资源保留供已打开的浏览器读取 |
| 邮件配置 | 复用现有 Notify SMTP 全组；新增 `IDENTITY_PUBLIC_BASE_URL=https://console.mengbin.top`。现有 `ServerAddress=https://api.mengbin.top` 为 Relay API 地址，未改写为控制台地址 |
| 策略 | `registration_enabled=true`、`email_verification=false` 保持原策略；`email_delivery_configured=true`、`registration_policy_available=true`。邀请码仍要求验证；Turnstile 保持关闭 |
| 公网 | Chrome 无登录会话实际访问注册、忘记密码与重置页通过；邀请验证字段、切换语言保留 token、离开清 token 通过，0 JS/资源错误。使用虚构 token 且不提交表单，没有创建账号、重置密码或再次发送邮件 |
| API 拒绝分支 | 公网无效邮箱与空重置请求返回 `success=false`、`Cache-Control:no-store`，确认新 BFF 路由到 Owner；没有执行有效挑战或账号写入 |
| 其他边界 | 其余 Compose 容器 ID/镜像保持，Billing 供应商报告监控仍为 `false`；历史费用 pending、1% 契约试点与原 FAIL/PAUSED 不变 |

回滚材料已保存在生产主机：

- 私有配置与部署清单：`/opt/micro-one-api/docker-compose/backups/a1-20261027-072232-4cae2d8e1ff2`（目录权限 0700）；仓库只记录路径与脱敏证据。
- 原镜像标签：`docker-compose-<service>:rollback-a1-20261027-072232-4cae2d8e1ff2`。
- 原前端：`/opt/web/dist.bak.a1-20261027-072232-4cae2d8e1ff2`。

需要回滚时恢复备份 Compose/环境配置，再以 `--no-deps --no-build` 重建 Identity/Admin 两服务，并将备份前端内容恢复到现有挂载目录；不要通过重命名整个目录替换已挂载的 inode。此次部署未修改注册策略或开启 Turnstile，不把关闭状态记为真实 Cloudflare 挑战已通过。

## 交付和回滚

本项无新增 SQL migration。先准备公网 URL 与现有 SMTP 环境变量，再更新 Identity、Admin API，最后发布 Web 静态目录；升级前核对三种配置模板与实际部署方式。Identity 配置与代码需配套上线，旧 Identity 没有公开策略接口时新 BFF 将关闭注册。存在历史重复邮箱时先经既有管理 Owner 核实绑定，不能直接选一行恢复密码。

所有 Identity 副本应使用同一 Redis，并完成新版本替换后再开放新注册/恢复入口。旧版本写入不执行新的邮箱占用检查，混跑期不视为完整验收。旧进程中的验证码不迁移到 Redis，升级后需要重新发码；正常登录会话兼容，成功重置的旧会话不会因回滚而重新有效。

生产镜像依仓库约定在本机/CI 交叉构建 Linux/amd64；Web 为宿主机独立发布，不能靠重建 Admin 镜像更新。回滚时同步回滚 Web/BFF/Identity，并视需要暂时关闭注册/恢复入口；已有 Redis challenge 有界到期。异步恢复任务不持久化，进程退出时尚未完成的请求需用户重新申请。

真实邮件已获收件人确认；真实 Turnstile 启用环境验收仍为外部保留项，A2–A4 未启动。

本次生产部署仅更新 Identity、Admin API 与 Web；不改变供应商证据监控停用、历史费用 pending、契约 1% 试点或原 executor FAIL/PAUSED 状态，不发布新版本/tag、不重开自动任务。
