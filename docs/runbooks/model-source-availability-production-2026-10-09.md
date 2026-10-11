# 模型可用来源禁用联动：生产更新记录

2026-10-09 · Asia/Shanghai · 最终验收时间 21:16:48。

channel-service、admin-api 已在本机交叉构建为 linux/amd64 并运输到生产；前端独立发布到宿主机 `/opt/web/dist`。源码摘要为 `f0d97c882eedfe74e1e89f87fe247b74b249ba689818b157c46a6d8cbf08aac5`，镜像标签 `deploy-20261009-205505-4c486b00` 中的提交号是构建基线；修复工作区通过该源码摘要标识。完整脱敏结果见[部署证据](evidence/model-source-availability-production-2026-10-09.json)。

## 根因与修复

原管理列表仅检查映射启用标志，没有检查渠道或订阅账号的状态，并直接展示 `models.status`。实际路由已经检查来源状态，因此线上出现“来源禁用、路由不可用，但管理模型仍显示启用且计数不减少”的差异。

模型读查询现在检查存活且启用的来源、启用的映射；来源计数按来源身份去重，供应商列表同步扣减。有效状态按全局来源判断，计数和映射仍遵循操作者的权限范围。数据库开关保持原值，来源恢复自动恢复有效可用性，手动禁用的模型继续禁用。

复审补齐 `configured_status`，使单行按钮能独立控制模型自身开关；列表和导出的可选 `status` 保留显式零值，在统计和分页前筛选。用量记录使用独立的模型 ID 查询，避免把管理详情的来源扫描和聚合带入每请求路径。

## 发布前后对照

使用正常登录获取的用户会话读取真实 Admin → channel 路径。渠道 9、订阅账号 4 在更新前均已禁用，本次验收没有启用、禁用或修改业务来源。

| 模型 | 更新前：状态 / 渠道数 / 订阅数 | 更新后：状态 / 渠道数 / 订阅数 | 有效来源 |
| --- | --- | --- | --- |
| `step-5-preview` | 启用 / 1 / 0 | 禁用 / 0 / 0 | 无 |
| `step-3.7-flash` | 启用 / 1 / 0 | 禁用 / 0 / 0 | 无 |
| `glm-5.3` | 启用 / 1 / 1 | 启用 / 1 / 0 | 渠道 1 |

三个模型的配置开关均保持 `configured_status=1`。来源全部失效时的有效禁用不会修改配置开关。来源恢复和手动禁用保留场景由数据库、内存及服务回归测试覆盖。

## 部署与验收

- 使用 `scripts/deploy-update.sh channel-service admin-api`，本机选用 `amd64builder`；服务器仅装载镜像和运行 Compose。
- 两个服务共用回滚标签 `rollback-20261009-205505`，旧镜像身份已经核对；固定部署标签已写入生产 Compose。
- 生产 Compose 中两个服务的旧源码摘要标签已备份并更正。最终镜像与容器的源码摘要一致，环境变量联合摘要与更新前相同。
- 两个服务运行正常，HTTP 健康检查通过，restart count 为 0，未发生 OOM。
- 真实列表、详情、启用/禁用筛选、禁用筛选导出结果一致；公开价格目录排除两个 step 模型，保留 glm-5.3。
- 前端 77 个发布文件逐文件核验，入口最后原子切换，保留旧 hash 资源供已打开页面使用。公网 `/admin/models` 的入口与引用资源均与宿主机发布文件一致。清理了 macOS 归档附带的 AppleDouble 元数据文件。
- 无数据库迁移，无业务来源、配置开关、凭证、IAM 授权或配额调整。

本地 `make test`、66 个前端测试文件的 335 项测试、完整前端 lint、生产构建和六项体积预算、架构边界及 831 行 RBAC 契约均通过。全量前端测试首次与镜像构建并行时有一项 5 秒超时，降低并发至 `--maxWorkers=2` 后全部通过，未增加超时阈值或跳过测试。新增回归覆盖来源归零、跨组去重、列表/详情、筛选/分页、导出、字段存在性和用量查询开销。

## 回滚材料

| 服务 | 当前镜像 ID | 回滚镜像标签 |
| --- | --- | --- |
| channel-service | `sha256:841fb815f2954af95e2d3874fbc07490d02dec30a3880873419db8e132b8a2e2` | `docker-compose-channel-service:rollback-20261009-205505` |
| admin-api | `sha256:2e12cc87d3d0cc748e72c18f1ef26bdc90295c930368ce29db367749a718223d` | `docker-compose-admin-api:rollback-20261009-205505` |

原 Compose 备份为 `$DEPLOY_REMOTE_DIR/docker-compose/docker-compose.yml.model-availability-before-20261009-210023`；脚本另保留 `docker-compose.yml.rollback-20261009-205505`。回滚时参考备份，仅恢复这两个服务的镜像和来源摘要字段，再在生产 Compose 目录执行 `docker compose up -d --no-deps --no-build --pull never channel-service admin-api`，避免覆盖其他服务后续修改。

前端备份为 `/opt/web/dist.bak.model-availability-20261009-210844`；恢复该备份的静态资源，最后切回旧 `index.html`，无需容器重启。镜像和前端应一起回退；回退后会重新出现本次修复的模型状态与来源计数问题。
