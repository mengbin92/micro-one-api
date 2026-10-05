# D1 生产 IAM 交接记录

> 2026-10-05 · 基于已合并 C 阶段的 `develop@0ab6d69d`；用户明确授权更新生产、替换 legacy、随后合并 develop。

生产已完成 `legacy/idle → legacy/blocked → iam/verified → iam/complete`。最终 policy revision **7**、catalog revision **2**，8 个账号全量核验通过；未知角色、身份映射差异、目录差异及约束冲突均为 0。普通授权从 IAM 分配、会话及固定目录取得，旧数字角色和共享令牌不再提供管理权限。

[脱敏实际证据](../../runbooks/evidence/rbac-d-cutover-2026-10-05.json) 包含逐服务镜像、状态报告摘要、成功审计 request/event ID、HTTP/gRPC 结果、数据库权限探针、财务与前端摘要。完整清点、备份、签名、配置和失败诊断留在远端 0700 私密部署目录；凭证、签名私钥及用户清单不进入 Git。

## 屏障与状态推进

维护窗口为 **08:28:40–09:48:04（Asia/Shanghai）**。宿主 3000/8080 入口返回 503，九个旧服务停止，在途数据库事务排空；旧远程 `root@%` 全部撤权、锁定并清除连接，实际旧凭证连接失败。数据库管理保留本地 root 通道，没有恢复共享 root 服务连接。

独立迁移账号只在此次交接启用，不进入普通容器；签名私钥只在本机使用。生产清单没有自定义角色、显式分配或受限委派，核准并签署空 manifest，无需 import。所有写阶段使用独立 request ID、batch 和最新 policy CAS，报告与成功审计同事务提交。

| 操作 | 结果 | policy revision |
|---|---|---|
| inventory/apply/shadow | 全部 8 个用户完成候选与固定目录核对，异常差异 0 | 3 |
| block | legacy/blocked，外部维护继续 | 4 |
| rebuild/verify | 最终全部用户重建及对账通过 | 5 |
| activate | iam/verified，普通写继续关闭 | 6 |
| complete/verify | iam/complete，全量 IAM 核验通过 | 7 |

首次 apply 已实际提交成功，但 ORM 的缺失回执诊断污染 stdout，包装器不能解析；通过状态和同事务审计确认成功，没有换 CAS 重用 request ID 再写。修复 CLI 日志后完成所有后续步骤。

## 实例、权限与财务

九个服务及迁移工具均在本机交叉构建 linux/amd64，校验运输摘要后装载；未在服务器构建。安装九个独立 outbound 凭证及固定 caller map，配置各 owner 数据库账号；普通服务没有迁移凭证、root/DDL 或 grant option。救援默认关闭。

billing 的 users 写限于 `balance/frozen_amount/used_amount/request_count` 四列；角色、状态、密码、身份版本、INSERT/DELETE 均实际拒绝。identity 以外的 owner 直接身份写也拒绝，共 **18 项实际权限探针**通过。已有 SQL SECURITY DEFINER 财务用户视图保留，definer 使用本地管理身份；另补 admin/billing 对 channel 模型、渠道、账号及路由组事实所需的最小只读视图/权限。这是生产分库基础设施补齐，没有新增业务迁移编号。

恢复流量前用户余额/冻结额/消费额/请求数、结算任务/预留状态与账本记录的联合摘要和排空时一致，待结算任务为 0，本次没有待人工重放任务。此证据证明已有数据库财务记录在窗口内未丢失或重复，不把上游未来回调当作已验证事项。

## 部署发现与修复

- 三套 Compose 的 `DATABASE_DSN` 与 `SQL_DSN` 同时使用 owner 覆盖。仅设置 SQL_DSN 会让从 config.yaml/Wire 取得的连接继续使用已锁定的共享 root；实际启动失败证明此遗漏，修复后九服务健康且 restart 0。预检增加两个连接一致性门槛及失败用例。
- `.dockerignore` 继续排除运行时凭证，明确保留构建所需 `scripts/tool-versions.env`；源码摘要也包含该版本锁定文件。首次缺文件构建失败后补齐，最终十个镜像全部成功。
- 离线 CLI 禁止 ORM 查询诊断混入 JSON stdout；真实子进程 SQLite apply 回归覆盖这条协议边界。
- 控制台 HTTP 从 Kratos 默认 1 秒改为有界 30 秒。IAM 配置列表逐项授权，生产曾在约 1.02 秒取消并返回 500；真实 HTTP 慢配置仓储测试先失败、修复后通过，线上完整配置读取再次通过。

状态切换签名绑定源码 `48b4f916…`；控制台期限修复后的九个最终镜像统一绑定 `f510db75…`，实际 image ID 见证据。之后仅为离线 CLI 的七个 operator-selected 文件路径添加 gosec 信任边界注释；去除这些新增注释与已部署文件逐字一致，不改变运行逻辑。入口源码摘要同步复审，gosec 通过。临时迁移账号已撤权锁定，并实际验证连接拒绝。

## 恢复与验收

前端独立更新 `/opt/web/dist`，保留原资源并更新入口；173 个发布文件摘要及实际返回 index 摘要核验通过。恢复 admin/relay 宿主发布端口后再次检查九服务健康、镜像、restart 0、前端和权限。正常 HTTPS API 的 `/healthz` 返回 200，TLS 验证通过；本机直接 IP:3000 探针得到 empty reply，前端验收来自宿主实际发布端口及容器实际返回内容。

**16 项线上 HTTP**：root 本人授权、IAM 目录/角色、用户/渠道/模型/路由组、配置、订阅与公开状态正常；普通用户本人授权可读；匿名、伪造 numeric-root 的普通用户、静态 ADMIN_TOKEN 管理访问拒绝。**5 项线上 gRPC**：专属 admin/root 管理成功，普通用户 numeric-root、共享 service token/root、无用户操作者、错误 caller/root 均拒绝。

最终运行源码的 `make verify`、828 行契约、IAM 全专项 race + 真实 Playwright、HTTP 超时回归通过；此前 D0 三库 fresh/repeat/race、切换故障及财务列隔离、七组浏览器证据保留。仅注释复审后补跑 CLI race、入口漂移门禁及 gosec。没有为本轮声称重新执行独立三库 negative/元数据升级脚本。

后续回滚仅使用已验证的 IAM 兼容镜像和前端；IAM 事实、审计及旧 DB 通道撤权保持。旧 B 二进制和 legacy 回填不能作为恢复手段。
