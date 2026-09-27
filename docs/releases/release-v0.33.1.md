# Micro-One-API v0.33.1 发布：Relay OTLP 导出修复与观测验收

> 2026-09-27 · 上一版：[v0.33.0](./release-v0.33.0.md)（2026-09-27）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.33.1)

v0.33.1 是 v0.33.0 之后的 **PATCH 观测可靠性版本**：修复 Relay 启用 OTLP 导出时的启动失败，补齐内部 Jaeger 持久化接收与 Grafana 查询配置，并完成 O5 Redis 隔离故障延迟复测。Q3 后续性能对照的基线同步到 v0.33.0。

**无公共 API/proto 变更、无数据库迁移、无前端构建变更**。受影响的运行时服务只有 `relay-gateway`；Jaeger 和 Grafana 属于可选的观测部署。生产已先行部署并验证 Relay HTTP span 导出，授权用户请求的 attempt／路由审计／trace 全链联查仍待一条实际请求。

## 修复与变更

### 1. Relay 启用 OTLP 后不再因资源 schema 冲突退出（O2）

**根因**：`InitTracer` 将 OTel SDK 默认资源的 schema 1.43.0，与代码固定使用的 semconv 1.24.0 资源合并。配置 `OTEL_EXPORTER_OTLP_ENDPOINT` 后，合并立即报 `conflicting Schema URL`，Relay 在发送任何 span 前退出。

**修复**：以无 schema 的稳定 `service.name` 属性合并资源；加入启用 exporter 后由真实本地 OTLP/HTTP 接收器收取 span 的回归测试。Compose 增加内部 Jaeger 2.20.0 receiver、持久化 Badger 卷与 48 小时 span TTL，Grafana 增加 Jaeger 数据源。collector 不发布宿主机端口；Relay 未配置 endpoint 时仍保持原有行为。

**影响服务**：`relay-gateway`，以及选择启用 OTLP 的 Jaeger/Grafana 观测部署。生产无认证请求的响应头 `X-OTel-Trace-ID` 已在 Jaeger 查得，span 的 `request.root_id` 与 `X-Request-ID` 一致；这类 401 请求不产生路由 attempt，不能代替授权请求的完整联查。详见[上线与回滚记录](../runbooks/o2-otel-export-2026-09-27.md)。

### 2. O5 Redis 故障延迟复测完成

**根因**：v0.33.0 发布时引用的早期隔离样本跨越重叠的 Prometheus 抓取窗口，无法严格比较健康、故障与恢复阶段的 RPC P95。

**修复**：按已修正的采样时序重新运行完整隔离矩阵，分别取得独立抓取窗口。V2 `GetAuthSnapshot` P95 为健康 4.60ms、故障 9.14ms、恢复 4.95ms；chat、结算及恢复路径均可用。更新运维记录与下一阶段状态，保留内部回退路径及熔断拒绝延迟尚未归因的边界。

**影响服务**：仅测试与运维证据；未改变生产缓存策略，也不把隔离结果外推为生产成本结论。详见[复测记录](../runbooks/o5-redis-fault-isolation-2026-09-26.md)。

### 3. Q3 后续性能对照基线更新

**根因**：v0.33.0 发布后，Q3 CI 仍以 v0.32.2 作为后续候选的对照版本。

**修复**：基线 tag 和 benchmark 文档更新为 v0.33.0，后续候选在同一 runner 上与最新已发布版比较。

**影响服务**：CI 性能门禁；无生产二进制行为变化。

## 兼容性说明

- 无公共 API/proto 变更、数据库迁移或前端资源更新。
- `OTEL_EXPORTER_OTLP_ENDPOINT` 仍是可选配置。启用后须先提供可达的 OTLP/HTTP receiver；现有生产 Compose 已指向内部 `http://jaeger:4318`。Jaeger 数据保留 48 小时，但 TTL 不是磁盘容量硬上限。
- Jaeger 固定 2.20.0，以兼容当前 Grafana 12.4.3 的 Jaeger 查询方式；升级任一组件前需重验数据源健康检查。

## 升级步骤

1. 核对现有生产 Compose 与 Grafana provisioning 配置并备份；生产文件在 `/opt/micro-one-api/docker-compose` 单独维护，不能直接以仓库模板覆盖。按[O2 运维记录](../runbooks/o2-otel-export-2026-09-27.md)添加内部 Jaeger、持久化卷和 Grafana 数据源。
2. 在本机交叉构建 `linux/amd64` Relay 镜像并传往服务器；先启动 Jaeger，确认内部查询 API 可用，再设置 `OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318` 并重建 Relay。生产已完成此步骤，保有原镜像及配置回滚备份。
3. 发一条请求，保存 `X-Request-ID` 与 `X-OTel-Trace-ID`，在 Jaeger 核对 span 的 `request.root_id`。授权请求还需从 routing audit 和 attempts 核对同一根请求；不要记录 API Key 原文。

## 验证

- `TestInitTracer_EnabledExportsSpan` 在修复前因 schema 冲突失败，修复后通过；本地真实 Relay 中间件向 Jaeger 导出成功，Jaeger 重启后仍能读取 Badger 中的 trace。
- 本次 `make verify` 全部门禁通过：格式、Go 单测与关键包 race、分层、迁移治理、生成 API 类型、前端 lint／测试／构建与字节预算；Compose 解析通过。直接运行 `go test ./...` 时，`test/e2e/suite` 因未启动其所需的本地 `localhost:3000` 服务栈失败；它不属于默认单元测试门禁。
- 生产 Relay `/healthz` 为 200，Jaeger、Grafana、Relay 均运行且无重启；上线后日志无 tracing 初始化或导出错误。生产 401 探测的 OTLP span 已按 trace ID 回查，Grafana 启动日志确认 Jaeger 数据源已载入。
- O5 修正采样后的隔离复测已通过；真实 Prometheus 告警自然 firing/resolved 与授权请求的完整 trace 联查仍需后续观察。

## 完整变更日志

- `47e3ca1d` chore(benchmark): advance Q3 baseline to v0.33.0
- `2b725fdb` docs: record O2 observation and O5 Redis fault retest
- `9aa5f21e` fix: enable production Relay OTLP export
- docs(release): v0.33.1
