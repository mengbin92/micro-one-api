# P3 运营工程生产更新

2026-10-14，接续用户“先完成工程，保留生产门禁”后的“更新线上环境，然后合并到 develop 分支推送”指令。工程提交 `e15d2464d74ca291b5b5925d404ad70d66f49096`，源码摘要 `1d28d8b51c51762491daa0df562cb037f762c0419443a11a3ceac302213e2fc1`。本次发布工程能力，保留真实费用和修复调用的现场关卡。

关联：[运营工程补齐](billing-upstream-p3-operations-completion-2026-10-14.md)、[完整部署证据](evidence/billing-upstream-p3-operations-deploy-2026-10-14/index.json)、[前次 P3 控制链部署](billing-upstream-p3-deploy-2026-10-14.md)。前次记录与工程验收索引分别描述当时状态；本记录是本轮生产更新结果。

## 发布内容与验证

identity-service、billing-service、admin-api 在 Apple Silicon 本地跨构建 linux/amd64，生产只加载镜像。统一不可变标签 `deploy-20261014-213705-e15d2464`。实际运行镜像的源码 label 与本轮摘要一致，三个健康接口均返回 `status=ok`，restart=0。

| 服务 | 实际镜像 ID |
|---|---|
| identity-service | `sha256:ac8423f52aaf5fd56252737fba7a2e12436b225d6fcd598df53d6bdc28fb4bd9` |
| billing-service | `sha256:d512dc61fc5747bd4f71a72523db93b95e6c099e8f221826ea19e9c310a0277f` |
| admin-api | `sha256:d97ec20e1597f1b7a355c275a98985703f155b8fd57676c4a8b8fbf6c2e788cb` |

宿主机 `/opt/web/dist` 已单独更新。保留原有不可变静态资源，最后原子替换 index，避免已打开页面找不到旧 chunk。线上 `/admin/vendor-reports` 返回的新 index 摘要为 `9913089fbef04471fc3dde72df68bbdee24dc4b3a25b0a566fa87370b4db40f6`；七个初始资源可获取，`VendorReportsPage-BB_qEuou.js` 的线上内容与发布文件摘要一致。未认证的费用报告 API 继续返回 401；未自动增加 IAM 授权或菜单配置。

Grafana 已通过既有目录 provisioning 加载 `supplier-evidence`，八个面板及查询与发布 JSON 一致，默认数据源仍为 Prometheus。文件摘要为 `04a33534999e6795178573998e47470775b2207f82b0a1726227cae45829dd7f`。生产 Grafana 使用 unified storage，已加载资源在 `resource` 表，旧 `dashboard` 表为空；初次验证只查询旧表产生假失败，改为检查资源的 `dashboard.grafana.app/dashboards`、name、spec 和 `classic-file-provisioning` annotations 后通过。没有重启 Grafana、修改其数据库或重置登录凭据。

工具镜像 `micro-one-api/upstream-evidence-tools:p3-ops-20261014` 已上传，实际 ID `sha256:f8614e61deda3e88bf617f4a3588d0dfebdbdf1e89ecc6bcc1ee937cb1bd019a`，平台 linux/amd64。cost、repair、weekly 三个 disabled plans 在生产主机以 network=none/read-only/cap_drop/no-new-privileges 运行离线检查；独立状态库初始化成功、任务 claim=0，没有调用供应商或模型。临时检查容器与输入已清理。

可选 `docker-compose.p3-operators.yml` 与 disabled 示例已复制至生产 compose 目录，三种 profile 的完整配置校验通过，既有默认 Compose 校验通过。没有启动生产 operator workers，没有提供调用凭据或启用计划。

## 生产边界

逐项对比部署前后开关、reviewer 配置存在性与探测范围：

- billing `BILLING_VENDOR_REPORT_MONITOR_V1=false`，实际 Prometheus 查询成功但没有观测序列，仍表示未启用或未观测。
- channel/relay/monitor 的 `UPSTREAM_CONTRACTS_V1_ENABLED=false`；reviewer keys 为空；relay 收费 allowlist 为空。
- P1 delivery/receipt/snapshot 与既有 probes 保持原值；monitor 的探测间隔、目标数量及目标集合摘要不变。
- channel、relay、monitor 保持原运行镜像；相关健康接口正常。relay outbox 241 条记录均 acknowledged，pending=0。
- 不运行新业务数据库迁移，不改历史账本、用户价格、供应商报告或模型授权。operator SQLite 独立状态仅在离线临时检查中创建。

真实凭据和账号范围、完整同窗 CSV/API、生产 reviewer/IAM/canary 预算、30 分钟/100 完整样本/fresh proof、五分钟切换收敛与真实通知链路、两周现场基线仍按[工程记录](billing-upstream-p3-operations-completion-2026-10-14.md)保留。部署不等于上述现场验证通过。

## 备份与回退

三个原服务镜像统一保留 `rollback-20261014-213705`，部署脚本保存了更新前 Compose 镜像配置。前端、默认 Compose、可选 profile 与既有配置备份位于 `/opt/micro-one-api/docker-compose/backups/p3-ops-20261014-134315`，目录 0700，含凭据的配置文件 0600，仅保留在生产。此附加备份的默认 Compose 已指向本轮服务镜像；服务回退应使用部署脚本的 `docker-compose.yml.rollback-20261014-213705` 或逐服务 rollback tag。

回退服务时先核对并恢复对应 image 字段，校验 Compose，再以 `--no-deps --no-build --pull never` 重建指定服务；不回退账本或 outbox。前端先恢复旧资源，再原子替换备份 index。新看板可移除该 provisioning 文件，既有其他看板保持原样。可选 profiles 始终关闭，旧工具镜像仍保留，无需执行 operator 作业。

本轮只合并推送 develop，不更新 main、不创建发布标签。
