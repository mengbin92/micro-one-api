# P1 生产部署：计时内存与供应商证据详情

日期：2026-10-20（Asia/Shanghai）。按维护者要求，先完成生产部署，再合入 develop。部署源码为 `44e443c3fb0139d177f4fd983077196ba037bb7e`，业务源码摘要 `5e78a87315f4ed72566915a87f3ad1e985e1b5bb3e8a2c51e01534c281d481e3`；之后仅补部署记录。实现与隔离容量验收见 [P1 实施记录](p1-memory-vendor-evidence-2026-10-20.md)。

## 部署与回退点

- 本地交叉构建 Linux/amd64，校验归档、镜像架构、源码标签及层摘要后传输；生产未构建镜像。
- 20:07 CST 仅应用 Log owner 尚缺的 `139_add_request_trace_snapshot_indexes`，确认 `idx_log_trace_snapshot(source,id)`、`idx_log_trace_user_snapshot(source,user_id,id)`；其余迁移均已应用。沿用 MySQL 容器本地 Unix socket 管理入口，未解锁 TCP root 或调整授权。
- 20:11:48–20:11:53 CST 按 Log → Relay → 宿主机前端顺序部署。镜像统一后缀 `deploy-20261020-194854-44e443c3`；完整镜像 ID 见证据。前端 80 个文件逐项核验，保留原哈希资产，最后原子替换 index，Admin 容器无需重启。
- 变更前已在生产私有目录 `/opt/micro-one-api/docker-compose/backups/p1-20261020-194854-44e443c3` 保留 Compose、环境、Log 库一致性备份、原前端及 Relay outbox 一致性副本；私有内容未下载或提交。旧 Log/Relay 镜像已标记 `rollback-20261020-194854`。上传归档与临时迁移 CLI 已清理，备份保留。
- 生产 Compose 仅更新两项镜像引用并同步 Jaeger 768 MiB。所有服务环境保持；Jaeger 和其余容器实例保持。

需要回退应用时，把两个服务镜像改回各自 `docker-compose-<service>:rollback-20261020-194854`，沿用 `docker compose up -d --no-deps --no-build --pull never <service>`；前端从上述私有备份恢复并最后替换 index。139 是兼容索引迁移，应用回退不要求删除索引；不自动恢复数据库或 outbox，避免覆盖部署后的业务写入。

## 生产验证

20:15:36 CST 最终核对 PASS：

- 九服务 `/healthz` 均 HTTP 200、重启计数 0、OOM false；十项环境摘要（含 Jaeger）与变更前一致。
- Jaeger 仍为 10:24:36 CST 启动的原实例，实际及 Compose 限额均为 805,306,368 B（768 MiB），未因本次部署重启。
- 旧 Relay → 新 Log、新 Relay → 新 Log 各发一条不带凭据的请求，均 HTTP 401，并确认摘要落库为 user 0、rejected、attempts/refs 0、claim 1。未调用真实模型或产生费用。
- 前端 HTTP 入口及供应商详情 chunk 与本地构建哈希一致，未认证报告 API 仍 HTTP 401。index SHA-256 为 `a5ec0f18e95b00f3b65611604fe0958ba2ee8434061f8e0e557447ed5e698cab`。
- Relay/Log 的已暴露 OTLP 队列丢弃与无效 carrier 计数为 0；Relay 已暴露计时导出计数 exported/queued 各 1。不存在的失败标签序列没有当作实测零值。outbox 5,246 条均 acknowledged，无待处理项。

[脱敏部署证据](evidence/p1-memory-vendor-2026-10-20/production-deployment.json)包含构建版本、备份摘要、迁移、混合版本探针、文件清单和最终服务状态；不含凭据、原始环境或业务数据库内容。该文件 SHA-256：`1f0244f29f8fdcb05e78bb85d5978b20092e4913b4ae466b33d49ce1ec9174d7`。

## 验收边界

本次是上线冒烟与部署一致性验证；前端八项浏览器验收来自先前隔离 fixture，未在生产登录浏览器核验真实账期。生产 Relay/Log 沿用既有未设容器硬内存上限的配置，未将隔离测试预算误写为生产限额；自然业务峰值、TTL 与生产 WS 仍须独立验收。

原 48h 观察 FAIL/PAUSED 保持，没有拼接新进程窗口、重启自动观察或扩大 executor 灰度。原观察任务未提交文件继续留在原工作区，不纳入本批合并；不发新版本或 tag，不变更钱包、价格或权限。
