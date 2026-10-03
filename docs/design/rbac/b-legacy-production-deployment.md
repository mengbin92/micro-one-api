# B0–B4 生产更新（保持 legacy）

> 2026-10-03 · 首轮构建 `rbac-b-7082bdcb`，范围修复 `rbac-b-7cc66b0b` · 更新后按授权合并到 `develop`。

本次按用户授权先更新线上环境，再提交、合并和推送 develop。全部镜像从本机固定源码快照交叉构建为 linux/amd64；源码 SHA256 为 `7082bdcb48f9ca5f9c8bd9a762100782f08c2d753633bea7bfb897c57ec8b2cc`，包含 1687 个非文档源文件。快照只包含 Git 跟踪或未忽略的源码，没有本地 .env、运行凭证或 node_modules。服务器没有构建镜像。

## 更新范围

identity、channel、billing、config、log、monitor、notify、admin、relay 共 9 个服务全部更新。线上实际镜像 ID 与本机清单逐项一致；健康检查 200、重启计数 0，服务器 Compose/.env 与各服务运行环境摘要保持一致。实际镜像 ID 记录在下方发布证据中。

前端继续从 `/opt/web/dist` 挂载提供。169 个本机构建产物逐文件校验，实际返回 index SHA256 与本机构建一致。先发布带内容摘要的静态文件，最后原子替换 index；保留旧静态 chunk 供已打开的页面继续加载。

## 推送门禁发现的整数范围修复

首次推送由仓库 pre-push 的 gosec 拦截，报告 12 处整数转换；没有跳过门禁。资源 uint64 版本转换为 int64 前校验上界，持久化版本转换为 uint64 前校验正数，兑换码 uint ID 通过既有 safecast 返回范围错误。回归测试先复现超范围 ID 被接受及 MaxUint64 与异常负版本匹配，修复后实际资源写入、配置 SQLite/内存删除和兑换码转换回归通过；三个受影响 data 包的完整 race 测试与全仓 gosec 均通过。

channel、billing、config 三个受影响服务使用本机固定快照重新构建 linux/amd64，标签 `rbac-b-7cc66b0b`。修复快照包含同样的 1687 个非文档源文件，摘要为 `7cc66b0b07a71a2180936bba75234ae5c38d575289c7520bb4a710aec091e18e`；按路径排序连接 UTF8 路径、NUL 与文件内容 SHA256 后再计算 SHA256。没有修改迁移、前端或线上配置。

修复镜像已校验上传摘要后依次上线。2026-10-03 08:30（Asia/Shanghai）最终复查：九服务实际镜像与清单一致、healthz 200、restart 0、启动错误 0；14 项业务/鉴权检查与 169 项前端摘要通过，线上配置及 legacy/idle 保持不变。完整 B0–B4 HTTP/gRPC 角色矩阵也已在构建完成后通过。修复清单、逐服务前后镜像与结果保存在原部署目录的 `overflow-7cc66b0b/` 和最终发布证据中。

## 迁移与授权状态

迁移前保存八个受影响分库的 single-transaction 完整快照，按 ownership 核对并应用 112–122 中本次所需项，共 18 次分库应用。每库重复执行返回 `nothing to apply`，状态查询没有待应用文件。

admin 的旧 `schema_migrations.applied_at` 是 BIGINT NOT NULL 且没有默认值。实际迁移和重复最小命令均在 metadata preflight 失败，业务 112 尚未开始。只为该列补齐其他分库已有的 `(unix_timestamp())` 默认值；类型、已有记录数量与值校验和均不变。此后实际迁移与 repeat 通过，既有元数据预检回归也通过。

更新后 SQL 复查仍为 `authorization_mode=legacy`、`cutover_state=idle`、policy/catalog revision 1；IAM 分配和非 draft/持久化 bound 权限为 0。没有切换 IAM 事实源或执行 C/D 阶段交接。

## 验证与回滚

9 个服务的 healthz、现有管理员 access、用户/渠道/模型、模型健康、配置、订阅组、订单读取与公开 status 均通过。匿名管理、本人入口和 relay models 返回 401；静态 ADMIN_TOKEN 不能调用普通 IAM 角色治理 API。验证只读取现有业务数据。

原运行镜像、受保护的服务器配置、数据库快照和原前端保存在 `/opt/micro-one-api/deploy-rbac-b-7082bdcb`；统一回滚标签为 `rollback-20261003-075314`。失败时按 before.json 恢复原镜像和原前端，逐服务以原 Compose 执行 `up -d --no-deps --no-build --pull never`。保留 additive 数据库结构；不把覆盖恢复运行中的业务数据库作为常规镜像回滚。

已清理任务上传的镜像/前端归档及临时迁移凭证文件；保留回滚镜像、数据库/配置/前端备份和脱敏验收记录。

[无凭证发布证据](../../runbooks/evidence/rbac-b-legacy-deploy-2026-10-03.json) 包含实际镜像、健康、配置保留、前端摘要、迁移结果与授权状态；[B 阶段闭合与本机验收](./b-execution-progress.md) 记录实际三库 race、真实角色矩阵及 make verify。本次不创建 main/tag 或版本 release。
