# Micro-One-API v0.9.3 发布：Kratos v3 升级 + buf 工具链迁移

> 2026-07-20 · 上一版：[v0.9.2](./release-v0.9.2.md)（2026-07-19）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.9.3)

v0.9.3 是一次**基础设施升级版本**，完成 v0.9.2 发版公告中预告的两项工作：

1. **Kratos v2 → v3 框架升级**：全部 9 个微服务及 `platform/` 共享库切换到 `go-kratos/kratos/v3`，并已在真实 x86_64 服务器上完成全链路部署验证。
2. **proto 生成工具链从 protoc 迁移到 buf**：统一使用 buf + buf workspace 管理 proto 生成与 lint，CI 与 Docker 构建同步接入，彻底移除 protoc 依赖。

同时修复了 3 个生产问题：admin-api 缺少 `SUBSCRIPTION_SCHEMA` 导致的订阅管理接口报错、日志 `Flush` 不等待在途条目导致的日志丢失、以及 relay 网关用量日志记录内部上游端点而非用户实际调用端点的问题。

本版**无新增业务表迁移**，**无 API 破坏性变更**，**无新增/删除端点**，所有变更均向后兼容，从 v0.9.2 平滑升级即可。

## 主要变更

### 1. Kratos v2 → v3 升级

- `go.mod` 全面切换到 `github.com/go-kratos/kratos/v3 v3.0.0` 与 `contrib/registry/consul/v3`，零 v2 残留
- `internal/server`、`platform/http`、`platform/registry`、`platform/config` 等全部 86 个文件完成 v3 API 适配
- 移除 v2 时代遗留的死代码（config/file import）
- **已验证**：在本地 arm64 通过 docker buildx + QEMU 交叉编译 9 个服务镜像（linux/amd64），部署到 x86_64 服务器后确认：
  - 9 个服务容器全部 Up，mysql/redis 健康，无崩溃重启
  - 二进制 strings 确认 `go-kratos/kratos/v3` + `consul/v3`，零 v2 引用
  - 端到端验证通过：healthz 200；admin BFF（users/channels/system-options/topup）；relay-gateway OpenAI 兼容 chat completions；一次真实 completion 后 billing 的 balance / used_amount / request_count 均正确变化

### 2. proto 生成：protoc → buf

- `buf.yaml` / `buf.gen.yaml` 统一管理全部 proto（`api/` + 9 个服务的 `internal/conf`），一个 buf workspace 覆盖全仓库
- 重构 proto 目录布局使 package 名与目录一致，9 个 `conf.proto` 共存于同一 workspace，消除 buf lint 豁免与 protoc 回退路径
- `Makefile`：`make api` / `make config` / `make proto` 全部走 buf，清理废弃变量
- CI（`.github/workflows/ci.yml`、`security.yml`）与 `Dockerfile` builder 同步接入 buf 工具链（版本与 go.mod 对齐固定）
- 删除 `third_party/` 下不再需要的 protoc 依赖 proto（google/api、openapi、validate 等，共减少约 2400 行）
- **注意**：`*.pb.go` 为 gitignore 的生成产物，开发者升级后需运行一次 `make init && make proto` 重新生成（kratos v3 需要 v3 版的 `protoc-gen-go-http` 插件，本地旧版 v2 插件生成的代码无法通过编译）

### 3. fix(deploy): admin-api 补充 SUBSCRIPTION_SCHEMA 环境变量

Admin 的订阅仓库读取 `user_subscriptions` / `subscription_groups` / `subscription_plans`，这些表在 schema 拆分后位于 `oneapi_billing`。缺少 `SUBSCRIPTION_SCHEMA` 时仓库回退到 `ADMIN_SCHEMA`，查询 `oneapi_admin.user_subscriptions` 报 "table doesn't exist"，导致 `/api/v1/admin/subscriptions` 不可用。relay-gateway 早已配置该变量，admin-api 遗漏，本版补齐（`deployments/docker-compose/docker-compose.yml`）。

**使用自有 compose 文件部署的环境请同步补上该变量。**

### 4. fix(log): BatchLogWriter.Flush 等待在途条目

此前 `Flush` 不等待已提交但仍在写入队列中的日志条目，服务关闭或刷盘时可能丢失尾部日志。现在 `Flush` 会等待所有在途条目落盘后返回。

### 5. fix(admin,relay): 用量日志记录用户实际调用端点 + 账本渠道信息补全

- **Relay 网关**：此前所有 Responses 回退路径（stream→anthropic、stream→chat、non-stream→anthropic、non-stream→chat）以及存储路由路径，都把内部上游协议端点（`/v1/messages`、`/chat/completions`）写进了用量日志。现在 `http_responses_handler.go` 全部 12 个回退点记录 `r.URL.Path`；`http_adaptor.go` 新增 `formatToEndpoint` 辅助函数，将 adaptor Format 枚举映射为真实入站路由，订阅账户路径正确记录 `/v1/chat/completions` | `/v1/responses` | `/v1/messages`。
- **Admin 账本**：渠道信息补全此前只接入列表路径，LogsPage 详情（`GetLedgerEntry`）始终只显示裸渠道 ID。抽取 `loadChannelEnrichments`（列表，单次 ListChannels RPC，带截断告警）与 `loadChannelEnrichment`（单条，GetChannel RPC）两条路径复用；`channelTypeToString` 改用 `relayprovider.ChannelType*` 常量替换 33 个魔法数字；ListChannels/GetChannel 失败改为经 applogger 记录而非静默吞掉。
- **LogsPage 详情面板**：移除无效的 'Source' 行。

## 升级步骤

```bash
# 拉取版本
git fetch --tags
git checkout v0.9.3

# 开发者环境：重装 pinned 工具链并重新生成 proto（pb.go 不入库）
make init
make proto

# 部署环境：重新构建镜像并滚动重启
docker compose build
docker compose up -d
```

**注意事项：**

- 使用自有 compose 文件的环境，请确认 admin-api 服务环境变量包含 `SUBSCRIPTION_SCHEMA`（值与 billing schema 一致，默认 `oneapi_billing`）
- 本地有旧版 `protoc-gen-go-http`（v2.x）的开发者**必须**执行 `make init` 重装 v3 版插件，否则生成的代码引用 kratos v3 不存在的 `transport/http/binding` 包导致编译失败
- 无数据库迁移，无需执行 `make migrate`

## 兼容性说明

- **API**：无破坏性变更，无端点增删
- **数据库**：无迁移
- **配置**：admin-api 新增必需环境变量 `SUBSCRIPTION_SCHEMA`（仅 schema 隔离部署；仓库 compose 文件已内置）
- **运行时**：kratos v3 为框架层升级，对外行为与 v0.9.2 一致，已在 x86_64 真实部署验证

## 验证

发布前已确认：

- `make build` 全量编译通过（buf generate + go build ./...）
- 全部单元测试通过（`go test`，排除需容器环境的 e2e 套件）
- x86_64 服务器真实部署：9 服务镜像（linux/amd64）全部 Up，端到端链路（admin BFF + relay chat completions + billing 扣费）验证通过
- 二进制确认零 kratos v2 引用

## 完整变更日志

- 5bf1b41 test(relay): assert user-facing /v1/responses endpoint in fallback commit quota
- 8f75af2 fix(admin,relay): log user-facing endpoint and enrich ledger with channel
- 4f723e9 docs(migration): record server x86_64 deployment verification for kratos v3
- e617e62 feat: upgrade kratos v2 to v3 and drop dead config/file import
- 9bf1852 refactor(migration): fully unify proto generation on buf, drop protoc
- 36f9831 chore(migration): finish buf lint + cleanup dead Makefile vars
- 4dbcabd fix(log): make BatchLogWriter.Flush wait for in-flight entries
- f8191cf chore(migration): wire buf into CI and Docker builds for proto generation
- c7b5b19 fix(deploy): add SUBSCRIPTION_SCHEMA to admin-api compose env
- 3809af3 feat(migration): migrate from protoc to buf for proto generation

## 下一步

后续版本计划：

- 按 `docs/migration/grpc-gateway-migration-todo.md` 评估带注解 HTTP 路由向 grpc-gateway 的迁移
- 完善 schema 隔离下跨 schema 读取场景的覆盖测试
- 加强用量统计与对账的可观测性

欢迎反馈与参与：[github.com/mengbin92/micro-one-api](https://github.com/mengbin92/micro-one-api)
