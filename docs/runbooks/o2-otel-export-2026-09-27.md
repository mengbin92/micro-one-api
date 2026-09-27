# O2：Relay OTLP 导出与受控存储

2026-09-27 在生产部署 Jaeger 2.20.0 单节点接收器，Relay 通过内部 Docker 网络的 `http://jaeger:4318` 导出 HTTP span。Jaeger 的 Badger 数据存于 `docker-compose_jaeger_data` 卷，span TTL 为 48 小时；4318 和 16686 均未映射宿主机端口。Grafana 12.4.3 从内部 `http://jaeger:16686` 查询。当前仅 Relay HTTP 入口生成 span，采样率为 1；不代表下游 gRPC 或供应商调用都有独立 span。

配置来源：[Jaeger 配置](../../deploy/jaeger/config.yaml)、[Compose 模板](../../deployments/docker-compose/docker-compose.yml)、[Grafana 数据源](../../deploy/grafana/provisioning/datasources/datasource.yml)。生产 Compose 单独维护在 `/opt/micro-one-api/docker-compose`，镜像引用已改为本机交叉构建后上传的 tag，挂载路径使用 `/opt/micro-one-api/monitoring/jaeger/config.yaml`。不能直接用仓库模板覆盖生产 Compose 中的其他服务配置。

Jaeger 2.21 移除了 Grafana 12.4.3 此处仍使用的 `/api/services` 查询接口；本地试验中 2.21 接收 span，但 Grafana 数据源健康检查为 400。固定 2.20.0 后，`/api/services` 与 Grafana 数据源健康检查均为 200。升级前要重新验证两者兼容性。[Jaeger release notes](https://github.com/jaegertracing/jaeger/releases)、[Grafana Jaeger 数据源文档](https://grafana.com/docs/grafana/latest/datasources/jaeger/configure/)。

## 验收结果

- 启用 exporter 前发现 `resource.Default()` 的 OTel schema 1.43.0 与代码固定的 semconv 1.24.0 冲突，Relay 启动会失败。先加入红色回归测试，再用无 schema 的稳定 `service.name` 属性合并资源；本地 OTLP 接收器实收 span。
- 本地真实 Relay 中间件向 Jaeger 导出 span 后，按 trace ID 查得 `request.root_id`；重启 Jaeger，Badger 中的该 trace 仍可查。
- 生产无认证 `GET /v1/models` 返回 401、`X-Request-ID=o2-prod-export-smoke-20260927`、`X-OTel-Trace-ID=ba0c778230e61c04827527d770119981`。在 Jaeger 查得 `HTTP GET` span，`request.root_id` 与 `request.id` 均为上述请求 ID。401 不会触发上游模型和路由 attempt，此证据只证明生产 HTTP span 的导出、存储和 ID 关联。
- 生产 Relay `/healthz` 为 200；上线后五分钟内日志无 tracing 初始化或导出错误。Jaeger 内部 `/api/services` 为 200，Grafana 启动日志确认插入 `uid=jaeger` 数据源。本地 Grafana 的 Jaeger 数据源健康检查为 200；生产 Grafana 管理员凭据不可用于自动健康 API 验证，所以不把生产数据源健康检查标为已通过。

脱敏、机器可读的上线记录见 [证据](./evidence/o2-otel-production-2026-09-27.json)。完整 O2 验收仍需一条授权用户请求：用其 `X-Request-ID` 查询 `/api/log/routing-audit?user_id=<id>&root_request_id=<ID>`，核对 attempt、最终 selection event 中的 `otel_trace_id`，再用该 ID 在 Jaeger 查 span 的 `request.root_id`。无认证探测不能代替该步骤；无需传递 API Key，只需请求 ID。

## 查询与回滚

在生产主机的 Compose 目录执行内部查询，传入响应头中得到的 trace ID：

```bash
docker run --rm --network docker-compose_backend alpine:3.20 \
  wget -qO- "http://jaeger:16686/api/traces/$OTEL_TRACE_ID"
```

Grafana 中选择 `Jaeger` 数据源，以 `relay-gateway` 服务和 trace ID 查询。对只读探测，可检查 `request.root_id`、`request.id` 和响应头；对授权请求再核对日志服务的 attempt / routing audit。不要把请求 ID 或用户 ID 放进 Prometheus 标签，也不要保存令牌原文。

48 小时 TTL 是保留时间，不是磁盘空间硬上限。运行期间检查 `docker stats jaeger` 与 `docker system df -v`，在流量明显增加时复核采样率和卷占用；当前生产首次观察约 16 MiB 内存、512 MiB 容器上限。

上线前的 Relay 镜像保存在 `docker-compose-relay-gateway:rollback-o2-20260927-073728`。生产配置备份为 `docker-compose.yml.bak.rollback-o2-20260927-073728` 与 `datasource.yml.bak.rollback-o2-20260927-073728`。若需回退，先将备份配置恢复到原位、将回滚镜像重新标记为 `docker-compose-relay-gateway:latest`，然后在生产 Compose 目录重建 Relay 和 Grafana。可停止 Jaeger 服务，但保留 `jaeger_data` 卷以便调查；不要用 `down -v` 清除其他服务的数据卷。
