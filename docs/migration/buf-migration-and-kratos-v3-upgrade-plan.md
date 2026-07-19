# buf 迁移 + Kratos v3 升级综合方案

> 状态：**buf 迁移（step 1+2）已落地**（2026-07-19，含 CI/Dockerfile 改造）；
> Kratos v3 升级（step 3）待执行。落地实现对原方案有少量偏差，见 2.3/2.4 末节。
> 评估日期：2026-07-19
> 评估基准：当前 `go.mod` 的 `go-kratos/kratos/v2 v2.9.3-0.20260413003801-0284a5bcf92b`
> （通过 `replace` 指向 Yanhu007 fork 规避 CVE-2026-6993）
> 关联文档：`docs/migration/grpc-gateway-migration-todo.md`（决策：不引入 grpc-gateway）

## 0. 结论先行

当前仓库同时面临两件事：

1. **proto 工具链切换**：从 `protoc` + 手维护的 `third_party/` 迁移到 `buf`，
   与同级目录 `example` 项目（`kratos new` 模板，已用 buf）对齐。
2. **Kratos 主版本升级**：`kratos/v2` → `kratos/v3`。

评估结论：**两件事相互独立但耦合，必须分两步走**。

| 步骤 | 时机 | 估时 | 风险 | 收益 |
|---|---|---|---|---|
| 1. buf 迁移 | 现在 | 1–2 人日 | 低 | 工具链标准化、`third_party/` 退役、版本锁定 |
| 2. Kratos v3 升级 | buf 落地稳定 1–2 周后 | ~3 人日 | 低–中 | 摆脱 Yanhu007 fork、对齐官方维护主线 |

**核心判断**：两个未知项（见第 4、5 节）都已确认为**利好**，
v3 升级的硬阻塞已不存在；但两步合并做会让回归失败难以二分定位，
**强烈建议分两个 PR**。

## 1. 评估背景

### 1.1 参考项目（同级目录）

`/Users/neo/vscode/mengbin/example` 是 `kratos new` 生成的模板，**已经使用 buf**：

| 维度 | example（buf） | micro-one-api（protoc） |
|---|---|---|
| 生成工具 | `buf generate` | `protoc` + 多个 `--proto_path` |
| Kratos 主版本 | **v3** | **v2**（Yanhu007 fork） |
| HTTP 生成器 | `protoc-gen-go-http/v3` | `protoc-gen-go-http/v2` |
| 第三方 proto | BSR 拉取 (`buf.build/googleapis/googleapis`) | 仓库内 `third_party/` 手维护 |
| 插件安装 | `go run xxx@version`（零安装） | `make init` 全局 `go install` 5 个工具 |
| 模块数量 | 2（`api`, `internal`） | **3 类共 10+ 个根**（`api/`, 8×`app/*/internal/conf/`, `internal/conf/`） |
| proto 文件数 | 2 | 19（10 api + 9 conf） |
| Windows 兼容 | 天然兼容 | 需要 Git Bash `find` 特判 |
| OpenAPI 校验 | `make api` 一步到位 | `make api-check` + 路径黑名单 |

### 1.2 当前仓库 proto 现状

- 10 个 `api/<domain>/v1/*.proto`：业务 API（OpenAI 兼容、admin、billing、channel、identity、log、monitor、notify、config、relay-gateway、common）。
- 9 个 `app/<svc>/internal/conf/conf.proto` + 1 个 `internal/conf/conf.proto`：服务级配置 proto。
- `third_party/` 维护 `google/api/`、`google/protobuf/`（隐式从系统目录解析）、`errors/`、`openapi/`、`validate/`。
- 跨 proto 引用：`api/admin/v1/admin.proto`、`api/billing/v1/billing.proto`、`api/channel/v1/channel.proto`、`api/identity/v1/identity.proto` 依赖 `api/common/v1/common.proto`。

## 2. 方案一：buf 迁移

### 2.1 目标

- 用 `buf generate` 替代当前 `Makefile` 的 `protoc` 多插件长命令。
- 第三方 proto（googleapis）从 BSR 拉取，删除 `third_party/google/`。
- 插件版本固化在 `buf.gen.yaml` 的 `local: ["go", "run", "xxx@vX.Y.Z"]`，团队/CI/Docker 生成结果一致。

### 2.2 配置文件规划

#### `buf.yaml`（根目录，buf v2 格式）

```yaml
version: v2
modules:
  - path: api
  - path: internal/conf
  - path: app/admin/internal/conf
  - path: app/billing/internal/conf
  - path: app/channel/internal/conf
  - path: app/config/internal/conf
  - path: app/identity/internal/conf
  - path: app/log/internal/conf
  - path: app/monitor/internal/conf
  - path: app/notify/internal/conf
deps:
  - buf.build/googleapis/googleapis
# 暂不启用 lint DEFAULT：现有 proto 用 created_at 而非 created_time、
# 缺少 error_reason.proto 等，DEFAULT 会大量报错。启用需单独排期。
lint:
  use:
    - MINIMAL
breaking:
  use:
    - FILE
```

> **注意**：`buf.yaml v2` 的 `modules` 需要把所有 conf 根目录列全，否则
> `buf generate --template buf.gen.config.yaml` 找不到这些 proto。

#### `buf.gen.yaml`（api 生成）

```yaml
version: v2
inputs:
  - directory: api
plugins:
  # buf 迁移阶段保持 kratos v2 插件，避免与 v3 升级耦合
  - local: ["go", "run", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11"]
    out: api
    opt: paths=source_relative
  - local: ["go", "run", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2"]
    out: api
    opt: paths=source_relative
  # kratos v2 版本，与当前 Makefile PROTOC_GEN_GO_HTTP_VERSION 一致
  - local: ["go", "run", "github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@v2.0.0-20260404020628-f149714c1d54"]
    out: api
    opt:
      - paths=source_relative
      - require_unimplemented_servers=false
  - local: ["go", "run", "github.com/google/gnostic/cmd/protoc-gen-openapi@v0.7.1"]
    out: .
    strategy: all
    opt:
      - fq_schema_naming=true
      - default_response=false
      - naming=json
```

#### `buf.gen.config.yaml`（conf 生成，仅 go）

```yaml
version: v2
inputs:
  - directory: internal/conf
  - directory: app/admin/internal/conf
  - directory: app/billing/internal/conf
  - directory: app/channel/internal/conf
  - directory: app/config/internal/conf
  - directory: app/identity/internal/conf
  - directory: app/log/internal/conf
  - directory: app/monitor/internal/conf
  - directory: app/notify/internal/conf
plugins:
  - local: ["go", "run", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11"]
    out: .
    opt: paths=source_relative
```

> conf 生成用单文件 `buf.gen.config.yaml` 的多 `inputs` 而不是
> 在根 `buf.gen.yaml` 里全量生成，是为了保持 `make config` /
> `make api` 的双目标语义不变。

### 2.3 Makefile 改造

对照 example 项目的 Makefile，1:1 套用：

```makefile
.PHONY: init
init:
	go install github.com/google/wire/cmd/wire@latest
	go install github.com/bufbuild/buf/cmd/buf@latest

.PHONY: config
config:
	buf generate --template buf.gen.config.yaml

.PHONY: api
api:
	buf generate --template buf.gen.yaml

.PHONY: proto
proto: api config
```

**可删除的旧逻辑**：
- `PROTO_SYSTEM_INCLUDE_DIRS` 系统头文件探测逻辑
- Windows Git Bash `find` 特判（buf 跨平台一致）
- `proto-tools` 目标（插件版本钉在 `buf.gen.yaml`）
- `PROTOC_GEN_GO_VERSION` / `PROTOC_GEN_GO_GRPC_VERSION` /
  `PROTOC_GEN_GO_HTTP_VERSION` / `PROTOC_GEN_OPENAPI_VERSION` 常量

> **落地偏差（2026-07-19 实测）**：`make config` 未改用 buf，仍走 `protoc`。
> 原因：9 个 `app/*/internal/conf/conf.proto` 与 `internal/conf/conf.proto` 同名
> （`conf.proto`），无法共存于同一个 buf v2 workspace（报 "contained in multiple
> modules"），多 `inputs` 又会被静默空生成；且 conf.proto 不 import 任何第三方
> proto，`protoc`（无 `third_party/`）即可满足。因此 `buf.gen.config.yaml` 是死配置，
> 已删除；`buf.yaml` 的 `modules` 只保留 `api`。`make init` 除 buf 外还安装 4 个
> protoc 插件（版本 pin 到 go.mod），`buf.gen.yaml` 用本地插件二进制而非 `go run`。

### 2.4 CI / Dockerfile 改造（已落地，2026-07-19）

实际实现与原方案有偏差，原因如下：

- **CI 保留 `protobuf-compiler`**：`make test-unit`/`make build` 依赖
  `make proto` → `make config`，而 conf 仍走 protoc（见 2.3 偏差），故 protoc 不能去掉。
  `make init` 现在安装 buf + 4 个 protoc 插件（版本 pin 到 go.mod）+ wire。
- **10 个 Dockerfile（9 服务 + 根 relay-gateway + deployments/docker）统一改造**：
  删除 `make proto-tools`，改为在内联 `go-deps` stage 里 `go install` buf + 4 插件到
  `/go/bin`，builder stage 只 `COPY --from=go-deps /go/bin/...` 拿预编译二进制。
  **未抽成共享外部 base 镜像**：CI 用 matrix 跑 18 个相互隔离的 buildx job
  （`push: false`、无 registry），无法跨 job 引用公共 base；强行引入 Docker Bake
  需重写 CI 并行/缓存/故障语义，收益不成比例，故保持每镜像内联 go-deps，靠
  BuildKit layer cache 去重。`deployments/docker/Dockerfile.deps`（外部 base）同步
  装 buf+插件，与内联 go-deps 一致，供 deploy.sh 离线预构建。
- **关键架构点**：`go-deps` 不加 `--platform=$BUILDPLATFORM`，与 builder 同 TARGET
  平台——工具链二进制由 builder COPY 并在其内执行，跨架构（amd64 runner 构建
  arm64）时若装在 $BUILDPLATFORM 会产生 builder 无法 exec 的二进制。
- **`protobuf protobuf-dev` 保留**：log/monitor/notify 的 conf.proto import
  `google/protobuf/duration.proto`，protoc 需 `protobuf-dev` 提供的 well-known type
  `.proto` 定义，删除会导致容器内 `make proto` 失败。

### 2.5 `third_party/` 处理

- `google/api/annotations.proto`、`google/protobuf/timestamp.proto`、
  `google/protobuf/duration.proto` 全部由 BSR 提供。
- **可删除** `third_party/google/`。
- `third_party/errors/`、`third_party/validate/`、`third_party/openapi/`：
  先 grep 确认没有 proto 仍 `import` 它们，再删。

### 2.6 风险与回滚

| 风险点 | 应对 |
|---|---|
| kratos v2 插件在 buf 下的 byte-level 差异 | 跑 `make proto` 后 `git diff` 对比，确认 `*.pb.go` 零变化或仅有可接受格式差异 |
| `make api-check` 的 openapi.yaml 校验 | 跑 `make api-check` 确认 `/v1/chat/completions` 路径仍存在 |
| CI「generated files are current」检查误报 | 生成代码 byte-diff 风险，需本地验证后合并 |
| 离线构建环境 | `buf.lock` 需要 `buf dep update`，Docker 构建走 `GOPROXY` 同理需网络 |

**回滚触发条件**：`make proto` 后生成的 `*.pb.go` 与现状有非空白字符差异，
且无法通过调整 `opt:` 消除 → 说明 kratos v2 插件与 buf 集成有坑，
回滚并先在 example 上用 v2 插件验证。

## 3. 方案二：Kratos v3 升级

### 3.1 API 接触面分析（已逐文件 grep 验证）

仓库内非生成代码只用到以下 v2 API 子集，**全部与 v3 兼容**：

| v2 引用 | 用到的 API | v3 兼容性 |
|---|---|---|
| `github.com/go-kratos/kratos/v2`（app） | `New / Name / Server / StopTimeout / BeforeStop / Registrar / App` | ✅ 完全一致 |
| `.../v2/config` | `New / WithSource / WithResolveActualTypes / Scan / Load / Close` | ✅ 完全一致 |
| `.../v2/config/file` | `_` 副作用导入 | ✅ 完全一致 |
| `.../v2/registry` | `Registrar / Discovery / ServiceInstance` | ✅ **逐字节相同**（已 diff） |
| `.../v2/transport/http` | `Server / NewServer / Address / Timeout / HandleFunc / HandlePrefix / NotFoundHandler / MethodNotAllowedHandler` | ✅ 全部保留 |
| `.../v2/transport/grpc` | `Server / NewServer / Address / Timeout / Options` | ✅ 全部保留 |

**关键发现**：项目**完全没有命中**官方迁移指南
（`docs/migration/v2-to-v3.md`）列出的 8 条 breaking changes：

- ❌ 没用 `kratos/v2/log`（用自己的 zap）
- ❌ 没用 `kratos/v2/middleware/auth/jwt`（v3 移到 contrib）
- ❌ 没用 `kratos/v2/encoding/*`（v3 拆分 json/protojson）
- ❌ 没用 `aegis` 断路器
- ❌ 没用 `transport/http/binding` 的公开 API（只在生成的 `*_http.pb.go`）
- ❌ 没用 `kratos.Logger()` 选项（v3 唯一改签名的 Option）

唯一命中项：生成的 `*_http.pb.go` 需要重新生成（v3 用
`http.SupportPackageIsVersion3`，v2 用 `Version1`）。
**这正是 buf 迁移的天然交汇点**。

### 3.2 业务代码改动（纯机械）

约 45 个非生成 `.go` 文件 + 15 个生成的 `.pb.go`，改动是字符串替换：

```sh
find . -name "*.go" -exec \
  sed -i 's|github.com/go-kratos/kratos/v2|github.com/go-kratos/kratos/v3|g' {} +
```

涉及文件分布：
```
 6  internal/server/
 4  cmd/relay-gateway/
 4  app/admin/cmd/admin/
 4  platform/config/
 3  platform/registry/
 3  platform/http/
 2×8 app/<svc>/cmd/<svc>/        （wire.go + main.go + config_loader.go）
 2×8 app/<svc>/internal/server/  （http.go + grpc.go）
```

### 3.3 生成代码重生成

v3 必须用 `protoc-gen-go-http/v3` 生成。如果 buf 迁移已完成，
只需在 `buf.gen.yaml` 里改一行插件路径：

```yaml
# 从 v2
- local: ["go", "run", "github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@v2.0.0-..."]
# 改成 v3
- local: ["go", "run", "github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3@v3.0.0"]
```

然后 `make api` 一次性重生成全部 `*_http.pb.go`。

### 3.4 删除 Yanhu007 fork replace

**前提**：`platform/http/kratos.go` 的 `SafeKratosServerOptions()` 已是
CVE-2026-6993 的代码级修复（见第 4 节），不再需要 fork。

`go.mod` 删除：
```diff
- replace github.com/go-kratos/kratos/v2 => github.com/Yanhu007/kratos/v2 v2.9.3-0.20260413003801-0284a5bcf92b
```

`platform/http/kratos.go` 仅更新注释（v2.9.2 → v3.0.0），
`SafeKratosServerOptions` 函数体保留，直到上游合并补丁。

### 3.5 Consul contrib v2 → v3

`platform/registry/registry.go` 改一行 import + `go.mod` 改一行 require：

```diff
- consulregistry "github.com/go-kratos/kratos/contrib/registry/consul/v2"
+ consulregistry "github.com/go-kratos/kratos/contrib/registry/consul/v3"
```

API 完全一致（见第 5 节验证），`New / WithHealthCheck / WithHeartbeat /
WithHealthCheckInterval` 等签名逐字相同。

### 3.6 风险与回滚

| 风险点 | 应对 |
|---|---|
| 业务代码 import 替换遗漏 | `grep -r "kratos/v2" --include="*.go"` 确认零命中再合并 |
| wire injector 重生成失败 | wire 生成器本身与 kratos 版本无关，只重生成 9 个 service |
| `platform/config/source.go` hot-reload 测试 | Source interface 在 v2/v3 一致，但仍需跑 fsnotify 集成测试 |
| 全量 e2e 回归 | OpenAI 兼容层、订阅扣费、admin BFF 三条链路必须跑通 |

## 4. 关键确认项 1：CVE-2026-6993 在 v3.0.0 的状态

### 结论：**未修复于 v3.0.0**

Kratos v3.0.0 的 `transport/http/server.go` 第 186-187 行**仍然 fallback 到
`http.DefaultServeMux`**，与 v2.9.2 完全相同：

```go
srv.router.NotFoundHandler = http.DefaultServeMux
srv.router.MethodNotAllowedHandler = http.DefaultServeMux
```

这正是 CVE-2026-6993 报告的 confused deputy 漏洞点。Yanhu007 fork 的
修复**尚未上游到 v3.0.0**。

### 对升级路径的影响：**好消息**

项目已经有一个 workaround，**不需要依赖上游补丁**。
`platform/http/kratos.go` 的 `SafeKratosServerOptions()` 正是该漏洞的
代码级修复，且在 v3 下完全可用（`NotFoundHandler` /
`MethodNotAllowedHandler` 两个 `ServerOption` 在 v3 完全保留，作用机制一致）。

已 grep 验证：**所有 9 个 service 的生产代码 HTTP server 构造都强制走
`SafeKratosServerOptions`**，没有任何一个 `khttp.NewServer()` 裸调用。
测试代码里的裸调用（`internal/server/*_test.go`）不暴露外部端口，
不受 CVE 影响。

### 迁移后变化

- **删除** `go.mod` 里的 Yanhu007 fork replace（fork 不再需要）。
- **保留** `platform/http/kratos.go` 的 `SafeKratosServerOptions`
  （CVE 修复，上游未合并前不能删）。
- 把注释里的 "Kratos v2.9.2 fallback" 改成 "Kratos v3.0.0 fallback"。

**反而比现状更干净**：不再依赖一个 fork，workaround 留在自己的代码里，
等上游合并后直接删 `SafeKratosServerOptions` 即可。

## 5. 关键确认项 2：Consul v3 contrib 可用性

### 结论：**已发布，API 完全兼容**

`github.com/go-kratos/kratos/contrib/registry/consul/v3` 已存在，
版本 `v3.0.0-20260626125723-668db92c2c00`（与 otel v3 contrib 同日发布）。

### 验证证据

**go proxy 返回有效 latest 版本**：
```
github.com/go-kratos/kratos/contrib/registry/consul/v3 v3.0.0-20260626125723-668db92c2c00
```

**v3 consul go.mod 锁定**：
```
module github.com/go-kratos/kratos/contrib/registry/consul/v3
require (
    github.com/go-kratos/kratos/v3 v3.0.0
    github.com/hashicorp/consul/api v1.34.2
)
```

**v2 → v3 公开 API 逐字一致**（已 diff `func (r *Registry)` 签名列表，
exit code 0）：

| API | v2 | v3 |
|---|---|---|
| `type Option func(*Registry)` | ✅ | ✅ |
| `func New(apiClient *api.Client, opts ...Option) *Registry` | ✅ | ✅ |
| `WithHealthCheck / WithHeartbeat / WithHealthCheckInterval` | ✅ | ✅ |
| `WithDeregisterCriticalServiceAfter / WithServiceCheck / WithTags` | ✅ | ✅ |
| `WithTimeout / WithDatacenter / WithServiceResolver` | ✅ | ✅ |

项目迁移成本：`platform/registry/registry.go` 改 1 行 import +
`go.mod` 改 1 行 require，**约 5 分钟**。

## 6. 执行顺序（最终推荐）

### 第 1 步：buf 迁移（现在，1–2 人日）

1. 写 `buf.yaml` / `buf.gen.yaml` / `buf.gen.config.yaml`，
   **插件保持 kratos v2**。
2. 改 Makefile（保留旧的 `proto-tools` 目标做 fallback，直到 CI 全绿）。
3. 跑 `make proto`，`git diff` 确认生成代码零变化
   （或仅有可接受的格式差异）。
4. 跑 `make api-check` 确认 `openapi.yaml` 一致。
5. 删除 `third_party/google/`
   （grep 确认 `errors/`、`validate/`、`openapi/` 无引用后再删）。

### 第 2 步：构建链更新（独立 PR）

1. 改 `ci.yml`，用 `bufbuild/buf-setup-action`。
2. 统一 9 个 Dockerfile 的 builder（借机抽 base image）。
3. 更新 AGENTS.md / README.md 开发环境说明。

### 第 3 步：Kratos v3 升级（buf 稳定 1–2 周后，~3 人日）

一次性完成三件事：
1. 业务代码 import 替换 `v2` → `v3`（约 45 文件）。
2. 删除 Yanhu007 fork replace（用 `SafeKratosServerOptions` 替代补丁）。
3. `buf.gen.yaml` 插件从 `protoc-gen-go-http/v2` 改成 `/v3`，
   consul contrib 从 `/v2` 改成 `/v3`。
4. `make api` + `wire ./...` + `go mod tidy`。
5. 跑全量测试 + e2e 回归。

### 为什么必须分步

| | 先 buf 再 v3 | 先 v3 再 buf | 同时做 |
|---|---|---|---|
| 风险隔离 | ✅ 出问题易定位 | ✅ | ❌ 一次失败难二分 |
| 代码冲突 | 有（两改 Makefile） | 有 | 无 |
| 工作量 | 中 | 中 | 小 |
| 推荐度 | ⭐⭐⭐ | ⭐⭐ | ⭐ |

buf 是 v3 的**前置依赖**（都涉及 proto 重生成），且 buf 风险更低、
收益更即时。两步走的风险隔离最好：buf PR 失败不影响 v3 决策，
v3 PR 出问题能立刻 `git revert` 回 v2（buf 与 v2 兼容，buf 配置不用回滚）。

## 7. 工作量汇总

| 阶段 | 工作项 | 估时 |
|---|---|---|
| buf 迁移 | 配置文件 + Makefile | 0.5 人日 |
| buf 迁移 | CI / 9 个 Dockerfile | 0.5–1 人日 |
| buf 迁移 | `third_party/` 清理 + `make proto` diff 验证 | 0.5 人日 |
| **buf 小计** | | **1.5–2 人日** |
| v3 升级 | 业务代码 import 替换（~45 文件） | 0.5 人日 |
| v3 升级 | Wire injector 重生成（9 个 service） | 0.5 人日 |
| v3 升级 | 删除 fork replace + 注释更新 | 10 分钟 |
| v3 升级 | Consul contrib v2→v3 | 5 分钟 |
| v3 升级 | Proto 重生成（含在 buf 改 1 行插件） | 含在内 |
| v3 升级 | `platform/config/source.go` hot-reload 测试 | 0.5 人日 |
| v3 升级 | 全量 e2e 回归 | 1 人日 |
| **v3 小计** | | **~3 人日** |
| **总计** | | **~5 人日** |

## 8. 不要做的事

- ❌ **不要在 buf 迁移 PR 里同时升级 kratos v3**（两件独立的事，混在一起
  出问题难定位）。
- ❌ **不要启用 `buf lint` 的 DEFAULT 目录**（现有 proto 用 `created_at`
  而非 `created_time`、缺少 `error_reason.proto` 等，DEFAULT 会大量报错；
  如要启用需单独排期整改 proto）。
- ❌ **不要在 v3 升级时删除 `platform/http/kratos.go` 的
  `SafeKratosServerOptions`**（CVE-2026-6993 未上游，删除会重新引入漏洞）。
- ❌ **不要在 v3 升级同时引入 `kratos/v3/log` 替换 zap**（迁移指南建议
  用 slog，但项目自有 zap 体系，混改会扩大回归面）。

## 9. 验收清单

### buf 迁移验收（2026-07-19 实测）

- [x] `buf.yaml` / `buf.gen.yaml` 存在（`buf.gen.config.yaml` 因 conf 同名冲突不可行，已删，见 2.3 偏差）
- [ ] `buf lint`（MINIMAL）通过 —— **未通过**：MINIMAL 含 `PACKAGE_DIRECTORY_MATCH`，
      现有 10 个 proto 的 `package api.<domain>.v1` 与 `api/` 模块根下的相对目录
      （`<domain>/v1`）不匹配，报 10 条。需加 lint `except`/`ignore` 豁免或后续整改，单独跟进
- [x] `make api` 生成的 `*.pb.go` 与迁移前 `git diff` 为空（实测零 diff，含 `*_http.pb.go` v2.9.2）
- [x] `make config` 生成的 `conf.pb.go` 与迁移前 `git diff` 为空（protoc 路径，10 个一致）
- [x] `make api-check` 通过（`openapi.yaml` 含 `/v1/chat/completions`，逐字节一致）
- [x] `third_party/` 已删除（含 `google/`、`errors/`、`openapi/`、`validate/`）
- [ ] CI 的 "generated files are current" 检查通过 —— 待本 commit 推送后验证
- [x] Dockerfile 构建成功 —— buildx 实测：`go-deps` 工具链安装 + billing 完整镜像
      `make proto` + CGO 编译 + 启动通过（linux/amd64）
- [ ] Windows 开发者按新 Makefile 能正常 `make proto` —— 未验证，需在 Windows 环境确认
- [x] 生成产物（`*.pb.go`、`openapi.yaml`）不入库：`.gitignore` 加 `*.pb.go`，
      误跟踪的 `internal/conf/conf.pb.go` 已 `git rm --cached`

### Kratos v3 升级验收

- [ ] `grep -r "kratos/v2" --include="*.go"` 零命中
- [ ] `go.mod` 无 Yanhu007 replace，require 指向 `kratos/v3 v3.0.0`
- [ ] `consul/v3` 出现在 `go.mod`，`consul/v2` 消失
- [ ] `make api` 生成的 `*_http.pb.go` 引用
      `http.SupportPackageIsVersion3`（不再是 Version1）
- [ ] `*_http.pb.go` 不再 `import "github.com/go-kratos/kratos/v3/transport/http/binding"`
- [ ] `go build ./...` 全绿
- [ ] `make test` 全绿
- [ ] `make test-e2e-local` 全绿（OpenAI 兼容层、订阅扣费、admin BFF）
- [ ] `platform/http/kratos.go` 的 `SafeKratosServerOptions` 保留且注释更新

## 10. 后续可选增强（不在本方案范围）

- `buf breaking`：启用后 CI 可对 API 契约做破坏性变更检查，对外部
  OpenAI 兼容协议有价值。建议 v3 升级稳定后单独评估。
- 删除 `SafeKratosServerOptions`：等 kratos 上游合并 CVE-2026-6993 修复后，
  在常规依赖升级 PR 里删除该 workaround。
- 引入 `kratos/v3/log`：评估是否用 slog 替换项目自有 zap 体系，
  但这属于独立的设计决策，不应在 v3 升级 PR 里混做。
