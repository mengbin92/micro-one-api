# P3 生产更新与 develop 合并

2026-10-14，按用户指令先更新线上，再合并 develop 推送。实现提交 `070c6a4578affda3c69ca97dc5c34a3c02d11640`；源码摘要 `5de45e642410229703b2d5b19b2c83636bc3c06959512f46af01eb0d7916a171`。关联：[P3 工程](billing-upstream-p3-2026-10-14.md)、[部署证据](evidence/billing-upstream-p3-deploy-2026-10-14/index.json)。未更新 main 或创建发布标签。

## 更新结果

billing-service → channel-service → admin-api → relay-gateway 四个服务按消费者在先顺序，在 Apple Silicon 本机通过标准 Dockerfile 跨构建 linux/amd64，生产只加载镜像。不可变标签为 `deploy-20261014-184908-070c6a45`，实际镜像 ID 与源码 label 均核对；四个健康接口返回 `status=ok`，restart=0。monitor-worker 保持原容器与已有探测预算。

P3 operator 工具镜像 `micro-one-api/upstream-evidence-tools:p3-20261014` 已上传，实际 ID 为 `sha256:ce73ede1b0cf02aeb0a5f6882074f9b66d32d1180b615ca477ead050fe05fb21`；四个工具使用 network=none 做入口检查，没有调用推理或供应商 API。两项新 Prometheus 告警已通过生产 promtool 校验并 reload，原有规则保留，当前共 49 条规则。

生产 Compose 使用 environment mapping 格式。首次配置补写使用了列表格式，Compose 校验阻止部署；恢复备份后改为 mapping，在替换前校验通过，没有因此重建容器。Prometheus 单文件 bind mount 使用保留 inode 的写入方式，reload 后查询实际已加载规则。

## 运行与门禁

- billing 明确设置 `BILLING_VENDOR_REPORT_MONITOR_V1=false`；尚无独立生产费用报告窗口，不自动制造空覆盖告警。
- channel reviewer public keys 为空；channel/relay/monitor 的 `UPSTREAM_CONTRACTS_V1_ENABLED=false` 保持原值，未发布新候选或自动恢复授权。
- P1 delivery/receipt/probe 开关保留，relay `RELAY_NATIVE_USAGE_V2_ALLOWLIST` 仍为空；没有扩大收费名单或探测预算。
- relay 持久 outbox 的 190 条记录均 acknowledged，pending=0。未认证管理请求读取 vendor reports 返回 401/403，供应商费用未对公共请求开放。
- 没有新数据库迁移，没有导入合成供应商报告，没有改用户价/账本/模型授权；前端没有变更，也未替换宿主机 web/dist。

真实 admin 权限/credential binding、同窗完整 CSV/API 和生产候选 30 分钟/100 完整样本/fresh proof 仍独立验证。工程部署不等于来源收费、模型公开或发票确认。

## 备份与回退

配置和告警备份保留于 `/opt/micro-one-api/docker-compose/backups/p3-20261014-104640`，目录 0700、文件 0600；其中配置含秘密，不进入 Git。四个原镜像统一保留 `rollback-20261014-184908`，Compose 亦有对应镜像配置备份。回退保留新表、账本、outbox 与证据，先暂停新准入并排空在途交付，不在生产编译。
