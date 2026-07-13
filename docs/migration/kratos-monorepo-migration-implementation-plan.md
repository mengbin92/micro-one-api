# Kratos 大仓模式迁移实施方案

> 依据 `docs/migration/kratos-monorepo-migration-plan-final.md` 与 `docs/migration/log-service-to-platform-logging.md` 制定。
> 本文面向当前仓库落地执行，不重复讨论是否迁移，而是明确迁移边界、服务映射、批次、CI 门禁和验收标准。

## 0. 结论先行

当前仓库已经具备单 `go.mod`、统一 `api/`、Kratos 风格 `biz/data/service/server/config` 分层的基础。实施重点不是重新生成所有代码，而是把现有 `cmd/<service>` + `internal/<domain>` 收拢到 Kratos 社区常见的 `app/<domain>/<type>` 大仓结构，并同步修正构建、部署、CI 和 import 边界。

本轮迁移以结构调整为主，但当前代码存在会阻止 `internal` 目录下沉的跨域源码依赖，因此必须先完成一个不改变对外行为的边界整理阶段。整体做五件事：

1. 清理或显式安置现有跨域源码依赖，使服务目录移动后仍可编译。
2. 服务目录迁移到 `app/<domain>/<type>`。
3. `internal/pkg` 按职责拆成 `platform/` 与 `pkg/`。
4. 更新 Makefile、Dockerfile、docker-compose、部署脚本、K8s、CI 中所有旧路径。
5. 增加架构边界检查，禁止重新引入跨服务实现依赖和基础设施反向依赖。

本轮不引入 `client/` SDK 封装层。只有当多个服务反复手写 retry、超时、熔断、鉴权注入等调用逻辑时，再单独评估。

## 1. 当前仓库判断

### 1.1 已经完成的基础

- `api/` 已是全局 proto 目录，例如 `api/identity/v1/identity.proto`、`api/relay/v1/relay.proto`。
- 各服务内部已经基本按 Kratos 层次组织，例如 `internal/identity/{biz,data,service,server,config}`。
- 根目录是唯一 Go module：`module micro-one-api`。
- Docker 构建已通过 `SERVICE_NAME` 构建不同服务，但入口仍是 `./cmd/${SERVICE_NAME}`。
- CI 当前对 backend、frontend、Docker 矩阵做全量验证，尚未按路径拆分。

### 1.2 与参考文档需要修正的点

参考文档提出过"如果 log-service 只是日志组件，则降级为 `platform/logging`"。当前仓库实际代码显示 `log-service` 已经承担独立业务职责：

- `api/log/v1/log.proto` 暴露 `ListLogs`。
- `internal/log/service/log.go` 提供日志查询和用户日志搜索 HTTP handler。
- `internal/log/data/data.go` 有 `List`、`ListByUser`。
- 部署配置中已有 `log-service` 独立容器、端口和依赖关系。

因此本轮迁移中 `log-service` 不降级，目标路径为 `app/log/service`。后续可以新增 `platform/logging` 作为通用日志库，但不能替代当前 `log-service` 的查询和存储职责。

### 1.3 阻塞目录迁移的跨域依赖

当前根级 `internal/` 对整个 module 可见，因此仓库里已经形成以下生产代码直接依赖：

| 调用方 | 被依赖实现 | 影响 |
|---|---|---|
| `internal/admin/service` | `internal/billing/biz` | billing 移入 `app/billing/service/internal` 后 admin 无法编译 |
| `internal/admin/service` | `internal/relay/provider` | relay 移入自身 `internal` 后 admin 无法编译 |
| `internal/admin/{server,service}` | `internal/subscription/biz` | subscription 不能简单归入 admin |
| `internal/billing/biz` | `internal/subscription/biz` | subscription 同时属于计费主链路 |
| `internal/relay/server` | `internal/subscription/biz` | subscription 同时属于网关扣费链路 |
| `internal/channel/{service,biz/oauth}` | `internal/relay/credential` | channel 依赖 relay 的凭据实现 |
| `internal/monitor/biz` | `internal/relay/provider` | monitor 依赖 relay 的 provider 实现 |
| `internal/pkg/validation` | `internal/relay/provider` | 所谓通用包反向依赖业务实现，不能直接迁入 `pkg/` |

Go 对 `app/relay/interface/internal/...` 的访问范围只允许 `app/relay/interface` 子树，并不是“同仓库都可访问”。所以这些依赖不是单靠 CI 才会发现，目录一移动，`go test ./...` 就会直接失败。迁移 PR 开始前必须先处理本节依赖，不能把问题留到 admin/relay 最后一批。

## 2. 目标目录

```text
micro-one-api/
├── api/
│   ├── admin/v1/
│   ├── billing/v1/
│   ├── channel/v1/
│   ├── common/v1/
│   ├── config/v1/
│   ├── identity/v1/
│   ├── log/v1/
│   ├── monitor/v1/
│   ├── notify/v1/
│   └── relay/v1/
├── app/
│   ├── admin/admin/
│   │   ├── cmd/admin-api/
│   │   └── internal/
│   ├── billing/service/
│   │   ├── cmd/billing-service/
│   │   └── internal/
│   ├── channel/service/
│   ├── config/service/
│   ├── identity/service/
│   ├── log/service/
│   ├── monitor/job/
│   ├── notify/job/
│   └── relay/interface/
│       ├── cmd/relay-gateway/
│       └── internal/
├── domain/                       # 明确共享的业务域，不放基础设施或通用工具
│   ├── subscription/             # 当前被 admin/billing/relay 共同使用
│   └── upstream/                 # provider/credential 等跨应用上游能力
├── platform/
│   ├── cache/
│   ├── config/
│   ├── database/
│   ├── events/
│   ├── grpc/
│   ├── http/
│   ├── logging/
│   ├── metrics/
│   ├── middleware/
│   ├── registry/
│   ├── security/
│   ├── tls/
│   ├── tracing/
│   └── websocket/
├── pkg/
│   ├── errors/
│   ├── safecast/
│   ├── safefile/
│   └── timeout/
├── cmd/
│   ├── admin-reset/
│   └── migrate/
├── deployments/
├── migrations/
├── scripts/
├── test/
├── web/
├── go.mod
└── Makefile
```

说明：

- `cmd/admin-reset`、`cmd/migrate` 是运维工具命令，不是长期运行服务，本轮可以继续保留在根 `cmd/`。如果后续工具增多，再统一迁到 `tools/` 或 `cmd/tools/`。
- `api/` 本轮不做大规模重排，避免 proto import path 与生成代码发生额外震荡。
- 每个服务内部仍保留 `internal/`。Go 编译器负责阻止其他服务直接 import；CI 再补充依赖方向和遗留路径检查。
- `domain/` 是基于当前仓库耦合现状增加的显式边界。它只承载确实被多个应用进程直接复用的业务能力，不能成为新的杂物目录。

## 3. 服务路径映射

| 当前入口 | 当前内部实现 | 目标路径 | 类型 | 本轮处理 |
|---|---|---|---|---|
| `cmd/config-service` | `internal/config` | `app/config/service` | service | 第一批打样 |
| `cmd/monitor-worker` | `internal/monitor` | `app/monitor/job` | job | 第一批 |
| `cmd/notify-worker` | `internal/notify` | `app/notify/job` | job | 第一批 |
| `cmd/channel-service` | `internal/channel` | `app/channel/service` | service | 第二批 |
| `cmd/billing-service` | `internal/billing` | `app/billing/service` | service | 第二批 |
| `cmd/log-service` | `internal/log` | `app/log/service` | service | 第二批，保留独立服务 |
| `cmd/identity-service` | `internal/identity` | `app/identity/service` | service | 第三批 |
| `cmd/admin-api` | `internal/admin` | `app/admin/admin` | admin | 第四批 |
| `cmd/relay-gateway` | `internal/relay` | `app/relay/interface` | interface | 第四批 |

`internal/subscription` 不是 admin 私有实现：当前 admin、billing、relay 都直接使用它，三个二进制也分别组装了其 usecase/repository。本轮为保持行为不变，推荐先迁到 `domain/subscription`，并明确 billing 团队为代码 owner；若要求严格的进程边界，则应另立项目把它收归 billing 或独立 subscription-service，并通过 API 调用，该方案不计入本轮结构迁移工期。

同理，`internal/relay/provider`、`internal/relay/credential` 被 admin、monitor、channel 直接引用。迁移前需把跨应用使用的最小集合抽到 `domain/upstream`；relay 独有的 adaptor、转发和调度实现仍留在 `app/relay/interface/internal`。`internal/pkg/validation` 目前只被 relay 使用且依赖 provider 类型，应迁入 relay 内部，不能列为通用 `pkg/validation`。

## 4. 平台层与工具层拆分

当前 `internal/pkg` 同时包含基础设施封装和纯工具，迁移时必须拆分，否则 `platform/` 会退化成另一个大杂烩。

### 4.1 迁到 platform/

迁入 `platform/` 的标准：依赖外部资源、协议、中间件或跨服务基础能力。

| 当前路径 | 目标路径 | 说明 |
|---|---|---|
| `internal/pkg/cache` | `platform/cache` | Redis、本地缓存封装 |
| `internal/pkg/db`, `internal/pkg/xdb` | `platform/database` | SQL 连接、事务、驱动差异 |
| `internal/pkg/events` | `platform/events` | 事件发布、订阅、消息模型 |
| `internal/pkg/grpc`, `internal/pkg/xgrpc` | `platform/grpc` | gRPC client/server 辅助 |
| `internal/pkg/xhttp` | `platform/http` | HTTP 客户端或 transport 辅助 |
| `internal/pkg/logger` | `platform/logging` | 通用日志库，不替代 log-service |
| `internal/pkg/metrics` | `platform/metrics` | Prometheus 指标 |
| `internal/pkg/middleware` | `platform/middleware` | HTTP/gRPC 中间件 |
| `internal/pkg/registry` | `platform/registry` | Consul、服务发现 |
| `internal/pkg/tls` | `platform/tls` | TLS 配置 |
| `internal/pkg/xconfig` | `platform/config` | 配置加载 |
| `internal/pkg/xtrace` | `platform/tracing` | 链路追踪 |
| `internal/pkg/websocket` | `platform/websocket` | WebSocket 基础封装 |

### 4.2 迁到 pkg/

迁入 `pkg/` 的标准：无外部资源依赖、无状态、可被任意服务复用。

| 当前路径 | 目标路径 |
|---|---|
| `internal/pkg/errors` | `pkg/errors` |
| `internal/pkg/safecast` | `pkg/safecast` |
| `internal/pkg/safefile` | `pkg/safefile` |
| `internal/pkg/timeout` | `pkg/timeout` |

### 4.3 暂不移动或需人工判断

| 当前路径 | 建议 |
|---|---|
| `internal/pkg/audit` | 若只是审计字段/纯模型，进 `pkg/audit`；若写库或发事件，进 `platform/audit` |
| `internal/pkg/auth`, `internal/pkg/crypto`, `internal/pkg/oauth` | 与鉴权、密钥、OAuth 外部交互相关，优先归入 `platform/security`，但需要逐包检查依赖 |
| `internal/pkg/model` | 如果是通用 DTO 或常量，进 `pkg/model`；如果是某服务领域模型，应迁回对应服务内部 |
| `internal/pkg/migrate` | 若只服务 `cmd/migrate`，可保留为 `cmd/migrate/internal` 或迁到 `platform/database/migrate` |

共享包迁移必须按“一个包或一组闭合依赖包 + 全仓 import 更新”原子提交。不能先把某个服务改为引用尚未创建的 `platform/cache`，也不能移动 `internal/pkg/cache` 后只更新一个服务；单 `go.mod` 下任一中间状态都必须保证 `go test ./...` 可通过。迁移顺序先用 `go list` 建立 `internal/pkg` 依赖图，再从叶子包开始，例如先迁 `safecast/safefile/timeout`，再迁依赖它们的 cache、middleware、grpc 等包。

## 5. 实施批次

### 5.1 Phase 0：解除 `internal` 下沉阻塞

目标：在不改变端口、服务发现名和对外 API 的前提下，消除第 1.3 节列出的跨域实现依赖。

任务：

1. 把 `internal/subscription` 迁到 `domain/subscription`，同步更新 admin、billing、relay 及三个 binary 的 wire 组装。
2. 把 provider/credential 中被 admin、monitor、channel、relay 共同使用的最小集合迁到 `domain/upstream`，relay 私有实现不外移。
3. 把 `internal/pkg/validation` 迁到 relay 内部；它当前只服务 relay 且依赖 provider 业务类型。
4. 消除 admin 对 `internal/billing/biz` 的直接依赖，例如把 `DecodePlanSnapshot` 所需的稳定值对象/解码逻辑放入 `domain/subscription`，admin 只依赖该稳定契约。
5. 新增 `scripts/check-architecture.sh` 并接入 CI，先约束现有目录，后续随 `app/` 迁移扩展规则。

验收：

```bash
make test-unit
rg '"micro-one-api/internal/(billing|relay|subscription)/' internal/admin internal/channel internal/monitor internal/pkg
rg '"micro-one-api/internal/subscription/' internal/billing internal/relay
```

上面的 `rg` 必须无结果；relay、billing 对共享 subscription 的引用应已改为 `domain/subscription`。如果团队不接受 `domain/` 共享业务包，则在此停止结构迁移，先完成 subscription/upstream 的服务化 API 改造。

### 5.2 Phase 1：原子迁移 platform/pkg

目标：先建立稳定的公共依赖路径，使后续服务目录移动不再重复改写 `internal/pkg` import。

执行规则：

1. 用 `go list` 和源码 import 生成 `internal/pkg` 包依赖图。
2. 从叶子包开始，每个 PR 迁移一个包或一组闭合依赖包。
3. 每次移动同时更新全仓所有消费者，不允许保留半数新路径、半数旧路径的状态。
4. `platform/`、`pkg/` 不得 import `app/`；`pkg/` 也不得 import `platform/` 或 `domain/`。
5. 每个 PR 均运行 `make test-unit`，并确认对应旧目录已没有 import 后再删除。

### 5.3 Phase 2：低风险服务打样

每个服务单独 PR，顺序如下：

1. `config-service` -> `app/config/service`
2. `notify-worker` -> `app/notify/job`
3. `monitor-worker` -> `app/monitor/job`

monitor 放在 notify 之后，是因为它原先依赖 relay provider，必须先确认 Phase 0 的 `domain/upstream` 边界可用。

单服务操作模板，以 `config-service` 为例：

```bash
mkdir -p app/config/service/cmd
git mv cmd/config-service app/config/service/cmd/config-service
git mv internal/config app/config/service/internal

rg 'micro-one-api/internal/config' app/config/service
rg 'micro-one-api/internal/pkg' app/config/service
```

然后将本服务 import 改为最终路径，并在同一个 PR 更新 Makefile、Docker matrix、所有 compose 变体及部署脚本中的该服务构建路径。不能等整批完成后再修构建系统，否则该 PR 无法独立构建和回滚。

Wire 文件随入口目录一起移动，修改 `wire.go` 后必须在新目录重新生成并审查生成差异：

```bash
cd app/config/service/cmd/config-service
wire
```

验证：

```bash
go test ./app/config/service/...
go build ./app/config/service/cmd/config-service
make test-unit
```

### 5.4 Phase 3：中等依赖服务

每个服务单独 PR：

1. `channel-service` -> `app/channel/service`
2. `billing-service` -> `app/billing/service`
3. `log-service` -> `app/log/service`

注意：

- channel 的 credential 引用必须已经指向 `domain/upstream`，并回归订阅账号 OAuth、密钥脱敏和 relay 调用。
- billing 的 subscription 引用必须已经指向 `domain/subscription`，并回归支付、订阅扣减、退款和对账。
- log-service 保留 gRPC/HTTP 服务、配置、部署和服务发现名，并运行 `TestLogIntegration`。

验证：

```bash
go test ./app/channel/service/...
go test ./app/billing/service/...
go test ./app/log/service/...
go test ./test/integration/... -run 'Test(Log|Relay|ChatCompletions)'
make test-unit
```

### 5.5 Phase 4：核心身份服务

迁移 `cmd/identity-service + internal/identity -> app/identity/service`。服务发现名、端口、环境变量保持不变，并验证 admin、relay、log 的 identity client 初始化路径。

仓库的 e2e suite 没有名为 `Identity` 的测试，不能使用 `-run Identity` 作为验收，否则会出现“没有执行任何测试但命令成功”。使用以下门禁：

```bash
go test ./app/identity/service/...
make test-unit
make test-e2e-suite
```

### 5.6 Phase 5：入口服务与管理后台

每个服务单独 PR：

1. `admin-api` -> `app/admin/admin`
2. `relay-gateway` -> `app/relay/interface`

注意：

- admin 的嵌入式前端路径同步更新 Makefile、Dockerfile 和 `//go:embed` 所在包路径。
- `domain/subscription` 保持独立共享域，不随 admin 移动。
- relay 是对外入口，必须跑 OneAPI 兼容接口、限额、渠道选择、透传、WebSocket 和订阅路径回归。

验证：

```bash
make web-build
go test ./app/admin/admin/...
go test ./app/relay/interface/...
make test-unit
make test-e2e-suite
```

## 6. 构建与部署改造

### 6.1 Makefile

把固定入口从 `./cmd/<service>` 改成服务映射表驱动。建议先加变量，不立即删除旧 target，等全部服务迁移完成后再清理。

```makefile
SERVICE ?= relay-gateway

SERVICE_PATH_relay-gateway := ./app/relay/interface/cmd/relay-gateway
SERVICE_PATH_admin-api := ./app/admin/admin/cmd/admin-api
SERVICE_PATH_identity-service := ./app/identity/service/cmd/identity-service
SERVICE_PATH_channel-service := ./app/channel/service/cmd/channel-service
SERVICE_PATH_billing-service := ./app/billing/service/cmd/billing-service
SERVICE_PATH_config-service := ./app/config/service/cmd/config-service
SERVICE_PATH_log-service := ./app/log/service/cmd/log-service
SERVICE_PATH_monitor-worker := ./app/monitor/job/cmd/monitor-worker
SERVICE_PATH_notify-worker := ./app/notify/job/cmd/notify-worker
SERVICE_PATH = $(SERVICE_PATH_$(SERVICE))

.PHONY: build-service
build-service:
	@test -n "$(SERVICE_PATH)" || (echo "unknown SERVICE=$(SERVICE)" && exit 1)
	go build -o bin/$(SERVICE) $(SERVICE_PATH)
```

`web-build` 需要从：

```text
internal/admin/server/static/web
```

改为：

```text
app/admin/admin/internal/server/static/web
```

### 6.2 Dockerfile

`deployments/docker/Dockerfile` 当前使用：

```dockerfile
RUN CGO_ENABLED=1 go build ... ./cmd/${SERVICE_NAME}
```

迁移后改为通过 `SERVICE_PATH` 构建，CI 矩阵为每个服务传入路径：

```dockerfile
ARG SERVICE_PATH=./app/relay/interface/cmd/relay-gateway
RUN CGO_ENABLED=1 go build \
      -ldflags='-s -w -extldflags "-static"' \
      -o /app/bin/service ${SERVICE_PATH}
```

admin 前端嵌入路径同步改为：

```dockerfile
RUN if [ "$SERVICE_NAME" = "admin-api" ]; then \
      rm -rf /app/app/admin/admin/internal/server/static/web && \
      mkdir -p /app/app/admin/admin/internal/server/static/web && \
      cp -a /tmp/web-dist/. /app/app/admin/admin/internal/server/static/web/; \
    fi
```

### 6.3 docker-compose 与 K8s

服务名、容器名、端口、环境变量保持不变，只改构建参数和配置路径。

仓库中至少有 `docker-compose.yml`、`docker-compose.lite.yml`、`docker-compose.postgres.yml` 三套服务 build 配置，同时 `scripts/deploy.sh`、`scripts/deploy-prod.sh`、`scripts/deploy-update.sh` 直接传递 `SERVICE_NAME`。每迁一个服务，都必须在同一 PR 为这些入口补充对应 `SERVICE_PATH`，不能只改主 compose 文件。

需要检查：

- `SERVICE_NAME`
- `SERVICE_PATH`
- `CONF_PATH`
- 健康检查路径
- `LOG_GRPC_ENDPOINT`、`LOG_HTTP_ENDPOINT`
- identity/channel/billing/config/log 的服务发现地址

### 6.4 CI

第一阶段先保留全量 CI，只加入架构边界检查和 Docker `SERVICE_PATH` 矩阵。等目录迁移稳定后，再引入路径过滤。

建议矩阵：

```yaml
matrix:
  include:
    - service: identity-service
      path: ./app/identity/service/cmd/identity-service
    - service: channel-service
      path: ./app/channel/service/cmd/channel-service
    - service: billing-service
      path: ./app/billing/service/cmd/billing-service
    - service: admin-api
      path: ./app/admin/admin/cmd/admin-api
    - service: config-service
      path: ./app/config/service/cmd/config-service
    - service: log-service
      path: ./app/log/service/cmd/log-service
    - service: monitor-worker
      path: ./app/monitor/job/cmd/monitor-worker
    - service: notify-worker
      path: ./app/notify/job/cmd/notify-worker
    - service: relay-gateway
      path: ./app/relay/interface/cmd/relay-gateway
```

迁移期间矩阵必须允许新旧路径并存：每个服务 PR 只切换自己的 `path`。Docker action 同时传入两个参数：

```yaml
build-args: |
  SERVICE_NAME=${{ matrix.service }}
  SERVICE_PATH=${{ matrix.path }}
```

后续路径过滤规则：

| 变更路径 | 验证范围 |
|---|---|
| `api/**` | proto + 全服务 build/test |
| `platform/**` | 全服务 build/test |
| `pkg/**` | 全服务 build/test |
| `domain/**` | 全服务 build/test + 相关集成测试 |
| `app/<domain>/<type>/**` | 对应服务 build/test/docker |
| `web/**` | frontend + admin-api build |
| `deployments/**` | Docker/K8s/compose 验证 |

## 7. 服务隔离门禁

Go 编译器本身会拒绝 `app/admin/admin` import `app/relay/interface/internal/...`。CI 脚本的职责不是重复模拟 `internal` 规则，而是补充以下架构约束：

1. `app/<domain>/<type>` 不得 import 另一个 `app/<domain>/<type>` 的任何实现包。
2. 已迁移 app 不得重新 import 根目录遗留的 `internal/<service>`。
3. `platform/`、`pkg/`、`domain/` 不得反向 import `app/`。
4. `pkg/` 不得 import `platform/` 或 `domain/`。

新增 `scripts/check-architecture.sh`。先让 `go list -deps ./...` 暴露编译和 `internal` 可见性错误，再基于每个 package 的直接 imports 检查额外规则；不要把 `go list` 错误重定向丢弃，否则脚本可能在分析失败时返回成功。

```bash
#!/usr/bin/env bash
set -euo pipefail

module_path="$(go list -m)"
violations=0

go list -deps ./... >/dev/null

while IFS='|' read -r package_path imports; do
  service_root=""
  case "${package_path}" in
    "${module_path}/app/"*)
      relative="${package_path#${module_path}/app/}"
      service_root="${module_path}/app/$(printf '%s' "${relative}" | cut -d/ -f1,2)"
      ;;
  esac

  for imported in ${imports}; do
    if [[ -n "${service_root}" && "${imported}" == "${module_path}/app/"* && "${imported}" != "${service_root}" && "${imported}" != "${service_root}/"* ]]; then
      echo "${package_path} imports another app implementation: ${imported}"
      violations=1
    fi

    if [[ "${package_path}" == "${module_path}/app/"* && "${imported}" =~ ^${module_path}/internal/(admin|billing|channel|config|identity|log|monitor|notify|relay|subscription)(/|$) ]]; then
      echo "${package_path} imports legacy service implementation: ${imported}"
      violations=1
    fi

    if [[ "${package_path}" =~ ^${module_path}/(platform|pkg|domain)/ && "${imported}" == "${module_path}/app/"* ]]; then
      echo "${package_path} has reverse dependency on app: ${imported}"
      violations=1
    fi

    if [[ "${package_path}" == "${module_path}/pkg/"* && "${imported}" =~ ^${module_path}/(platform|domain)/ ]]; then
      echo "${package_path} is not a pure utility package: ${imported}"
      violations=1
    fi
  done
done < <(go list -e -f '{{.ImportPath}}|{{join .Imports " "}}' ./app/... ./platform/... ./pkg/... ./domain/...)

exit "${violations}"
```

CI 中加入：

```yaml
- name: Check architecture boundaries
  run: ./scripts/check-architecture.sh
```

## 8. 单服务迁移检查清单

每个服务 PR 必须附上以下检查项：

```text
服务名：
目标路径：

[ ] cmd/<service> 已迁入 app/<domain>/<type>/cmd/<service>
[ ] internal/<domain> 已迁入 app/<domain>/<type>/internal
[ ] 迁移前已确认没有其他目录直接 import 该服务的 internal 实现
[ ] import 已从 micro-one-api/internal/<domain> 改为新路径
[ ] 公共依赖只使用 domain/、platform/ 或 pkg/ 的最终路径
[ ] 已在新入口目录重新运行 wire，wire.go / wire_gen.go 路径可编译
[ ] go test ./app/<domain>/<type>/... 通过
[ ] go build ./app/<domain>/<type>/cmd/<service> 通过
[ ] make test-unit 通过
[ ] Makefile 对该服务的新路径可用
[ ] Docker build 对该服务的新路径可用
[ ] 所有 compose 变体、部署脚本和 K8s 仍保持原服务名、端口、环境变量
[ ] scripts/check-architecture.sh 通过
[ ] 与上游/下游服务联调通过
[ ] 旧目录已删除
```

## 9. 回滚策略

每个服务迁移独立 PR；共享 domain/platform/pkg 按闭合依赖组独立 PR，避免一次性大爆炸变更。

回滚优先级：

1. 单服务 PR 未合并：直接关闭 PR，不影响其他服务。
2. 单服务 PR 已合并但未发布：revert 该 PR。
3. 已发布后发现问题：镜像 tag 回退到迁移前版本；由于服务名、端口、注册名保持不变，部署层可以直接回滚。
4. `platform/` 拆分引发公共依赖问题：优先回滚具体公共包迁移，不回滚已经稳定的服务目录迁移。

不得在同一个 PR 中同时迁移多个核心服务和公共基础设施，否则回滚边界不清晰。

## 10. 总体验收标准

全部迁移完成后，以下命令和流程必须通过：

```bash
make proto
make web-build
make test-unit
./scripts/check-architecture.sh
make test-e2e-suite
docker build --build-arg SERVICE_NAME=relay-gateway --build-arg SERVICE_PATH=./app/relay/interface/cmd/relay-gateway -f deployments/docker/Dockerfile .
```

部署验收：

- docker-compose 全量环境可启动。
- admin-api 前端静态资源正常嵌入。
- relay-gateway 对外 OpenAI/OneAPI 兼容接口行为不变。
- identity、channel、billing、config、log 的 gRPC/HTTP 地址不变。
- log-service 查询接口仍可用。
- monitor-worker、notify-worker 定时/异步任务仍可运行。

文档验收：

- README、Makefile、scripts、部署文件和仍在维护的 runbook 中，所有 `cmd/<service>`、`internal/<domain>` 旧路径已更新；历史 release note 可以保留历史路径。
- 所有 compose 变体和部署脚本均传递正确的 `SERVICE_PATH`。
- 新增服务规范明确使用 `app/<domain>/<type>/cmd/<binary>`。

## 11. 建议 PR 拆分

1. `refactor/monorepo-domain-subscription`：迁移共享 subscription 域并消除 admin 对 billing biz 的直接依赖。
2. `refactor/monorepo-domain-upstream`：提取共享 provider/credential，收回 relay 私有 validation。
3. `refactor/monorepo-guards`：新增架构检查脚本和 CI 门禁。
4. `refactor/monorepo-platform-<group>`：按依赖拓扑分组迁移 `internal/pkg`，每组全仓更新 import。
5. `refactor/monorepo-config`、`refactor/monorepo-notify`、`refactor/monorepo-monitor`：每个服务独立 PR。
6. `refactor/monorepo-channel`、`refactor/monorepo-billing`、`refactor/monorepo-log`：每个服务独立 PR。
7. `refactor/monorepo-identity`：迁移 identity-service。
8. `refactor/monorepo-admin`、`refactor/monorepo-relay`：分别处理 web embed 和对外入口联调。
9. `refactor/monorepo-ci-path-filter`：结构稳定后引入按路径触发，并清理过渡期兼容配置。

## 12. 工作量预估

| 阶段 | 内容 | 预估 |
|---|---|---|
| Phase 0 | subscription/upstream 边界整理、架构门禁 | 4-7 人日 |
| platform/pkg 拆分 | 依赖拓扑、分类迁移与全仓 import 修正 | 4-6 人日 |
| 第一批 | config、notify、monitor 打样 | 3-4 人日 |
| 第二批 | channel、billing、log | 4-6 人日 |
| 第三批 | identity | 2-3 人日 |
| 第四批 | admin、relay、web embed、入口联调 | 4-5 人日 |
| CI 路径过滤 | paths filter、Docker matrix、文档收尾 | 2 人日 |
| 合计 | 结构迁移 + 边界整理 + 验证 + 文档 | 23-33 人日 |

如果 Phase 0 选择把 subscription/upstream 改造成严格的独立服务 API，而不是使用 `domain/` 共享业务包，需要单独设计 proto、事务边界、调用失败语义和数据归属，预计额外增加 5-10 人日，不应混入上述结构迁移 PR。
