# 证据与上游契约管理页面生产更新

2026-10-15 开始，2026-10-16（Asia/Shanghai）完成。按用户“补充缺失的内容，然后上线，然后再合并到 develop 分支并提交”的指令，补齐管理入口、只读授权和真实 IAM 读取链路，并更新生产制品。

功能提交 `4df5b623`，Owner 授权覆盖修复 `6308298665995caf8abbf8d38e915666e2ba525d`。部署源码摘要为 `10eb1614f240b0b12055e5709a5443f865ffaceab5490e954c9356e77de0211b`。完整脱敏结果见[验证索引](evidence/evidence-management-pages-deploy-2026-10-16/index.json)。

## 根因与补齐

1. 供应商费用核验页面已经发布，但迁移 134 只安装 `draft` 权限目录，既有 root 没有对应 grant，导航因此隐藏。迁移 136 仅发布 `billing.vendor_report.read`、`channel.upstream_contract.read` 两项读取权限，并增量授予既有 builtin root；不新增用户绑定、不覆盖显式 deny，不恢复治理中已禁用或归档的权限。更新 policy/catalog/root revision，以固定审计 receipt 保证重复执行不重新启用后来被禁用的权限。
2. 上游契约及候选修复只有管理 API，没有前端。新增 `/admin/upstream-contracts`，提供精确来源/模型/协议查询、当前绑定与灰度/CAS 版本、不可变历史、提案/独立重放/CI/审核签名证据；候选创建及发布、晋级、回滚、恢复、隔离按各自权限显示并执行。兼容 ProtoJSON 与 legacy 的整数和零值省略，权限变化清理数据，409 不重试，写成功但回读失败时暂停进一步写入。
3. 真实管理员读取验收发现两 API 均返回 403：`owner execution point incomplete or caller denied`。Billing/Channel 的 CoverageUnaryInterceptor ready 名单漏了已实现的 5 项 operator RPC，请求没有到达 Owner 授权。补齐登记并通过真实 Operator/Coverage 拦截器链回归；非法调用身份、未知方法、Owner 自身拒绝及 system-only probe/result 继续拒绝。没有通过放宽用户授权解决传输拦截问题。

新菜单位于“计费与财务 → 供应商费用核验”和“资源与路由 → 上游契约与修复”。已有 root 刷新后台即可加载新入口；其他角色没有自动新增授权。供应商报告和所验精确范围目前均无记录，页面保持“未知/暂无记录”，不填造验收数据。

## 生产制品与顺序

所有服务在 Apple Silicon 本地跨构建 linux/amd64，生产只加载镜像，未在生产编译。先执行 Billing 135，再执行 Identity 136，随后按 Identity → Billing → Channel → Admin → Relay → Monitor 更新。最终统一标签 `deploy-20261015-225036-63082986`；复用 Channel 镜像的 account-ops worker 随后同步。

| 服务 | 实际运行 image ID |
|---|---|
| identity-service | `sha256:ac7f6914fec8fccda83b3a8847c8600e4564829f7c2b4895c7fd342b97df87eb` |
| billing-service | `sha256:6883ff9f04572c7c8853f1f524a956ada06065bc070001083f9785b94adfcd42` |
| channel-service / channel-account-ops-worker | `sha256:8a02209ff7b325bfb3c3f80ece9ba83a1183ac7560266a8b57cb40fee477600d` |
| admin-api | `sha256:57c65b46c86bdef800158c857a8b19025e39fc09031e456782e53adcd8ec49cd` |
| relay-gateway | `sha256:3fc259b34aa33981c18ea8fbd3ae7b82dc4d0ebaecaf10ed7dbdc0087cb57455` |
| monitor-worker | `sha256:f15886f5a34057f437024d76f57c558998230df9ef16f28400b93aee4ce369a1` |

迁移使用本机既有正式 linux/amd64 静态 migrate 制品，其 `cmd/migrate`、xdb 与 runner 核心相对 P2 构建基线未变化；通过 MySQL 本地 socket 管理通道按 ownership 执行。135/136 均已有 schema_migrations 记录，重复执行返回 `nothing to apply`。运行身份没有新增 DDL 或全库权限。

前端于北京时间 **2026-10-16 03:44:59** 独立发布到宿主机 `/opt/web/dist`：保留旧哈希资源，校验并复制新资源，最后原子替换 index。index SHA256 为 `1e66598e9a7c9d1ab202b15b3a2a960c50db6f56273c502b5574ea67afae0993`，入口为 `index-B8ri2JF9.js`；未通过重启 Admin 来更新前端。

account-ops worker 通过既有 `account-ops-shards` profile 单独更新。该服务 extends Channel，重建时发现会额外继承后来增加的 `UPSTREAM_PROBES_ENABLED`、`UPSTREAM_CONTRACTS_V1_ENABLED`、`UPSTREAM_REPAIR_REVIEW_KEYS`。在 worker 专属 environment 中用 Compose `!reset null` 移除这三项继承，再重建并验证：其完整环境和门禁摘要均与部署前完全一致。该进程保持 scan-only 路径，不启动通用契约 canary。

## 验证结果

- 标准单元测试 gate 172 个包通过；5 个服务内 integration 包通过。新增 Owner 覆盖修复的 Billing/Channel server、authz 和 RBAC checker 4 包回归通过。
- 前端 85 文件、532 项测试、完整 lint、TypeScript、生产构建和 6 项体积预算通过。覆盖菜单/路由权限、中文原因、精确 CAS、权限撤销、回读失败禁写及 legacy 首次创建/复制/发布。
- 分层检查、863 项 RBAC 入口契约、迁移治理和 diff whitespace 检查通过。MySQL 8.0/8.4、PostgreSQL 16 全量迁移及 repeat 通过；135 的 MySQL 8.0 历史排序回填、唯一序号拒绝及 repeat 通过。136 的 SQLite/MySQL 8.0/PostgreSQL 实测覆盖显式 deny、禁用阻断、失败回滚、receipt 重放及后续禁用保留。
- 7 个运行进程均为 linux/amd64、实际镜像源码摘要匹配、healthz HTTP 200、restart=0，Compose 镜像与实际容器一致。7 份完整环境及门禁摘要逐项等于部署前值。
- 通过正常管理员登录会话读取授权：两项 read 为 true，5 项新增写权限均为 false；真实供应商报告 API 和上游契约 API 均返回 200，原 403 反馈环已转绿。未认证读取仍返回 401。
- 公网两个页面均返回新 index，7 个初始资源及两个页面 chunk 的内容摘要逐项与发布制品一致。Relay outbox 923 条均 acknowledged，pending=0，复审所需持久字段已存在。

裸 `go test ./...` 会包含依赖独立预启动服务的 `test/e2e/suite`；本次该外部 suite 因未启动其环境而连接被拒绝，不计作通过。上述标准 unit gate 按仓库规则排除此 suite；本次现场验收使用真实生产只读 API。Mac 锁屏，未补做真实浏览器点击和视觉验收，不把 HTTP/资源校验描述为浏览器点击通过。

## 保留的生产边界

本次只读目录发布不启用报告导入/归因、契约创建/发布/隔离权限。供应商费用监控、上游契约执行开关、收费 allowlist、探测目标与预算、可选 cost/repair/weekly profiles 均保留原值。没有为验收发起付费推理、导入真实供应商费用或修改用户价格、历史账本。

真实供应商同窗账期、费用差异、reviewer、生产 canary/灰度窗口和收费扩面仍需各自独立证据；源码更新和页面可读不代替这些关卡。

## 备份与回退

变更前完整 Identity/Billing/Channel 数据库、Compose、宿主机前端和一致性 SQLite outbox 备份位于生产 `/opt/micro-one-api/docker-compose/backups/evidence-management-20261015-221603`，目录 0700，含秘密文件 0600，仅保留在生产。原始业务镜像和两次部署的 rollback 标签均保留。

全服务回退以该原始备份为准；`rollback-20261015-225036` 中部分服务指向已更新过的第一轮构建，不将其误认为全部服务的原始版本。回退保留新表、账本、outbox 和迁移审计，按消费者能力处理在途证据，不删卷或回退财务数据。前端先恢复备份资源，再原子替换 index。只读权限撤回通过正常 IAM 治理禁用，不能重新执行旧迁移试图回退授权。

新增 Operator/Coverage 回归将真实 IAM 传输链纳入验收；今后新增管理入口需要同时验证会话授权、Owner RPC 可达和实际 API 结果，不能只用页面 shell 200 或健康检查证明功能交付。
