# D 阶段迁移工具、演练与生产交接

> 2026-10-04 · 基线 `develop@0ab6d69d`；工作分支 `codex/rbac-d-cutover`。
> D0 本机实现与演练已交付。D1 尚未执行；生产只读预检发现专属服务凭证、旧数据库写通道退出与能力版本证据尚未满足。没有将生产状态改为 IAM，也没有将待执行项标为完成。

## 实现

唯一离线入口为 `app/identity/cmd/iam-migrate`。cmd 只组装 identity biz/data；业务逻辑在 `iam_migration.go`，驱动、PO、SQL、主库连接在 data。工具只读取明确设置的 `IAM_MIGRATION_DSN`，不回退到 `SQL_DSN` 或 `IDENTITY_SQL_DSN`，不运行 bootstrap、Redis、token 回填或自动 DDL。

| 子命令 | 门槛与结果 |
|---|---|
| `status`、`inventory` | 同一主库一致视图，读取状态/全部用户/候选差异/目录与约束/自定义关系清单；不写会话或授权 |
| `apply` | 仅 legacy/idle 的独立迁移通道；按数据库 `0/1/10/100` 映射候选，发布本版固定目录与内置 grant 清单；未知 role 整批拒绝 |
| `shadow` | 固定旧操作矩阵与实际 source/scope 算法比较本人、其他用户、共享组事实；区分预期安全收紧与异常差异；不改变业务响应或修补 grant |
| `block` | 外部屏障、排空、旧进程/旧 DB 通道退出、财务方案、兼容回滚、实例能力均须有 root 核准证据；policy CAS 后持久化 blocked/batch |
| `rebuild` | 仅 legacy/blocked，重建全部用户候选，覆盖新增/降级/删除/未请求用户；再做全量对账；失败保持 blocked |
| `import` | 仅 legacy/blocked，导入签名 manifest 的自定义角色、三类委派和显式分配；上下文、scope、保护项、引用、CAS、图及未来约束重新验证；失败整批回滚 |
| `verify` | 只读核对所有用户、目录发布、root、关系与未来约束；IAM 后不再按数值角色做对账 |
| `activate` | blocked 下再次完整 rebuild、对账和 shadow；所有自定义关系须与 root 核准 manifest 双向一致；同事务记录 verified/时间/iam 与报告审计，普通写仍关闭 |
| `complete` | 仅 iam/verified，复核 IAM 事实与前端/实例/屏障证据后推进 complete；才恢复新写 |
| `resume` | blocked 继续完整 activate；verified 只继续 IAM 核验/complete；complete 不重复写；不同 batch、旧回填和反向切回 legacy 永久拒绝 |
| `manifest-digest`、`keygen`、`sign-evidence` | root 操作者离线计算 manifest 摘要、生成/签署证据；新文件 0600、拒绝覆盖；私钥不交给迁移进程，无数据库写 |

写入复用既有单 policy 行锁、CAS、SQLite 忙重试和事务成功审计。成功报告与状态同事务保存于 `iam_audit_events`，无需新增迁移表。固定 `request-id` 的相同请求返回已提交回执；同 ID 换内容拒绝。失败回滚后独立记录失败审计，审计失败仍拒绝。

只删除/替换 `origin=legacy_candidate`。现有同角色 `default/bootstrap` 行受数据库唯一约束保护，保留原 ID、来源和时间，并作为同等映射纳入核对；不能为迁移改写其来源。保留额外身份分配不等于认可越界权限：旧 root 不符实际 role、内置管理员残留、自定义关系未获 root 核准或约束不满足均阻断。已删除用户的候选与会话依赖清理，审计不删除。

`iam_migration_catalog.go` 固定枚举本版 root/platform_admin 操作；member/guest 保留既有自助路径，并明确授予本人会话设施。新增 code 不自动进入历史内置角色。原 root-only 模型/价格交换权限不授予 platform_admin；认证邮箱/凭证接管和路由授予收紧单独报告，不为消除差异扩权。未绑定/组织操作不发布。root 的执行豁免也必须有本版 root grant，不靠 `root=true` 隐式获得新增 operation。

真实切换回归发现 A6 的 IAM 管理适配器只检查服务认证标记，导致 IAM 模式下共享 token 仍可携带 root JWT 调用管理方法。修复为检查已验证的专属 principal、真实 full method 与固定 caller 清单；HTTP 本人认证保留，legacy 兼容仍受原模式门槛。A6 正向用例改用专属 admin 凭证，新增共享 token 拒绝反例。

## 生产准备与执行流程

详细停写、财务与恢复步骤见 [D1 执行手册](../../runbooks/rbac-iam-cutover.md)。`scripts/rbac-cutover-preflight.py` 只读检查实际 9 个容器，凭证留在远端进程内，输出存在性、指纹、caller 一致性、DB 用户与能力镜像摘要，不输出 token/密码。它只能检查可观测前提，不能替代 DB GRANT 审查或证明外部屏障已经建立。

`scripts/prepare-rbac-service-identities.py` 只创建新的 0600 凭证覆盖文件，不改现有 `.env` 或生产容器。实际运行已生成本机私密草案并通过 `check-service-identities.py --env` 的独立性/接收方清单核验；凭证不进入 Git；`.dockerignore` 排除运行时环境文件，镜像构建不携带这些凭证。三套 Compose 增加各服务 `*_SQL_DSN` 覆盖，以支持不同 DB 凭证；空值保持原连接。救援有独立 token 和默认关闭开关，普通 IAM API 不能开启。

证据由独立 root 审批私钥签署，包含 batch、root 用户、源码 SHA256、数据库通道指纹、manifest 摘要、24 小时内的有效期，以及屏障/排空/旧写者退出/撤权/财务重放/回滚/前端/九类实例/强制回归引用。CLI 同时核对签名、实际 root、已编译源码摘要和所选迁移数据库通道；签名不是普通 IAM 权限或绕过业务规则的凭证。

## 验收证据

- SQLite/MySQL/PostgreSQL 实际隔离库：fresh/repeat 全量既有迁移后执行 D0 race；候选后新增、降级、删除、未请求用户，非候选来源保留，未知 role 阻断，审计故障全回滚，跨批候选重建、丢响应回执和 manifest 导入幂等/保护项回滚/未来成员数约束，verified 故障仍停写，verified 后禁止旧回填，恢复后不按数值 role 放权。
- 实际 HTTP/gRPC：经工具走 apply → blocked → verified → complete；停写阶段注册与授权入口拒绝；真实旧 JTI 惰性会话、自助默认角色、root 管理、伪造数字角色及共享 service token 反例。
- MySQL 实际 billing 仓储：旧写者事务先排空，再撤销 UPDATE/INSERT/DELETE；旧连接不能写角色/密码或复活账号。独立财务列凭证仍能写余额/冻结额/消费计数，不能写角色、状态、凭证、身份版本或创建/删除用户。该证据只来自本机临时库，不能算生产撤权完成。
- CLI：签名篡改、未知/尾随 JSON、服务 DSN 不回退、私钥文件权限、拒绝覆盖、格式化证据签名规范化。
- 原 C 阶段真实 Playwright 7 组角色场景重新通过，无授权响应 mock。
- 生成/Wire、828 行入口契约、全仓 `make verify`、三套 Compose 配置渲染及凭证核验；复验命令见下。额外全容器构建 smoke 遇到本机 Docker 内存不足（编译进程被终止），未计为通过。

```sh
make all
make wire-check
make rbac-contract-check
make verify
python3 scripts/test-rbac-cutover-preflight.py
python3 scripts/check-service-identities.py

# 两个 DSN 必须指向本机临时实例；测试自动创建/销毁独立 schema。
GROUP_CONTEXT_TEST_MYSQL_DSN='<local disposable mysql>' \
GROUP_CONTEXT_TEST_POSTGRES_DSN='<local disposable postgres>' \
go test -race ./app/identity/internal/data ./app/identity/cmd/iam-migrate \
  ./app/billing/internal/data -run 'TestIAMMigration|TestIAMDMySQLFinanceColumnIsolation' -count=1

go test -race ./internal/integration \
  -run 'TestIAMDActualCutoverHTTPRPC|TestIAMA6RealAdminIdentityEndpoints' -count=1
IAM_C_PLAYWRIGHT=1 go test ./internal/integration \
  -run '^TestIAMCRealBrowserMatrix$' -count=1 -v
```

没有新增 DDL；本轮三库使用既有完整迁移 fresh/repeat 创建 scratch 库。未重新执行独立的 MySQL/PostgreSQL negative/元数据升级脚本，不将其计作本轮新增验收。各 DSN 未设置时测试明确 skip，不能计为三库通过。

## D1 尚未完成的真实门槛

2026-10-04 只读清点：生产 policy 为 legacy/idle、policy/catalog revision 均为 1；共 8 个账号（guest 2/member 5/root 1），无未知 role。9 个业务服务仍运行 B 阶段镜像；专属 outbound token 均为空，接收方 caller map 均为空；SQL 连接均为共享 root。[脱敏生产预检](../../runbooks/evidence/rbac-d-preflight-2026-10-04.json) 保留实际镜像与阻断项。实际部署前需要生成并一起安装 9 个专属凭证与 receiver map，配置各 owner 的最小权限 DB 通道，撤销旧进程/脚本通道并排空，准备 IAM 兼容镜像/前端和独立迁移通道，再核准 manifest 与停写窗口。

目前没有生产屏障/退出/撤权/前端能力/完整对账的成功证据，没有签署可执行的生产证据文件，因此不能将 `block/activate/complete` 的本机测试当成生产交接。D1 执行后必须另存实际实例、DB 权限、停写与重放、全量报告、状态推进和恢复结果；本记录不会先行写成生产 IAM 已启用。
