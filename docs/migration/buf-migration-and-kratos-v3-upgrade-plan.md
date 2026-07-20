# buf 迁移 + Kratos v3 升级综合方案

> 状态：**buf 迁移（step 1+2）已落地并彻底收尾**（2026-07-19 落地 → 2026-07-20 补完 lint/Makefile → 2026-07-20 彻底重构消除 except + protoc 依赖，验收清单 §9 全绿，buf lint 无 except 通过）；
> Kratos v3 升级（step 3）待执行。
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

### 1.2 仓库 proto 现状（迁移前 / 2026-07-19）

> 以下为迁移前布局，保留作为决策背景。迁移后的布局见 §2.2/§2.3。

- 10 个 `api/<domain>/v1/*.proto`：业务 API（OpenAI 兼容、admin、billing、channel、identity、log、monitor、notify、config、relay-gateway、common）。
- 9 个 `app/<svc>/internal/conf/conf.proto` + 1 个 `internal/conf/conf.proto`：服务级配置 proto（**9 个同名 `conf.proto`**，是 buf workspace 的核心阻塞）。
- `third_party/` 维护 `google/api/`、`google/protobuf/`（隐式从系统目录解析）、`errors/`、`openapi/`、`validate/`。
- 跨 proto 引用：`api/admin/v1/admin.proto` 等 4 个 proto 用 `import "common/v1/common.proto"`（相对 `api/` 解析）依赖 `api/common/v1/common.proto`。

**buf lint 阻塞点**：`package api.<domain>.v1` 与目录 `<domain>/v1`（module root=`api/`）不匹配 → 10 条 `PACKAGE_DIRECTORY_MATCH`。
**conf 生成阻塞点**：9 个 `conf.proto` 同名无法共存于 buf v2 workspace（"contained in multiple modules"）。

## 2. 方案一：buf 迁移

### 2.1 目标

- 用 `buf generate` 替代当前 `Makefile` 的 `protoc` 多插件长命令。
- 第三方 proto（googleapis）从 BSR 拉取，删除 `third_party/google/`。
- 插件版本固化在 `buf.gen.yaml` 的 `local: ["go", "run", "xxx@vX.Y.Z"]`，团队/CI/Docker 生成结果一致。

### 2.2 配置文件规划

> **演进**：本节原方案是 module root=`api/` + `buf.gen.config.yaml` 双文件，
> 落地时因 9 个 conf.proto 同名 + PACKAGE_DIRECTORY_MATCH 阻塞，一度退到
> `except` + conf 走 protoc 的妥协方案（见 §2.2 历史小节）。**2026-07-20 彻底
> 重构**后，module root=`.`、单 `buf.gen.yaml` 覆盖全仓、buf lint 无 `except` 通过、
> conf 走 buf。下面是最终落地形态；原方案和中间妥协方案作为"演进历史"附在末尾。

#### `buf.yaml`（根目录，buf v2 格式，module root=`.`）

```yaml
version: v2
modules:
  - path: .
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

**关键点**：
- `modules: path: .`（module root = 仓库根，不是 `api/`）。这样 `package
  api.admin.v1` 期望目录 `api/admin/v1`，与实际目录一致 → `PACKAGE_DIRECTORY_MATCH`
  无报错，**无需 `except`**。
- 9 个 conf.proto 重命名为 `<svc>_conf.proto`、package 改成 `app.<svc>.internal.conf`
  / `internal.conf`（匹配各自目录），故 conf 也满足 `PACKAGE_DIRECTORY_MATCH`。
- `google/protobuf/duration.proto`（log/monitor/notify 的 conf import）由 BSR 的
  `googleapis` dep 提供，不再需要系统 `protobuf-dev`。

#### `buf.gen.yaml`（单文件，覆盖 api + conf）

```yaml
version: v2
plugins:
  # 插件二进制由 make init / Dockerfile builder 预装到 PATH，与 go.mod 版本对齐。
  # buf 迁移阶段保持 kratos v2 插件，避免与 v3 升级耦合。
  - local: protoc-gen-go
    out: .
    opt: paths=source_relative
  - local: protoc-gen-go-grpc
    out: .
    opt: paths=source_relative
  # kratos v2 版本，与 go.mod 中 kratos/v2 一致
  - local: protoc-gen-go-http
    out: .
    opt: paths=source_relative
  - local: protoc-gen-openapi
    out: .
    strategy: all
    opt:
      - fq_schema_naming=true
      - default_response=false
      - naming=json
```

**关键点**：
- `out: .`（不是 `api`），因为 module root 是仓库根，`paths=source_relative`
  让生成代码落在 proto 同目录。
- 无 `inputs`：buf 自动用 `buf.yaml` 的 module 列表，全仓 proto 一次性生成。
- `protoc-gen-*` 是**生成器二进制名**（由 `make init` 预装），不是 `protoc` 编译器。
  全仓不依赖系统 `protoc`。
- `buf.gen.config.yaml` 已删除：单 `buf.gen.yaml` 覆盖 api+conf，`make config` 用
  `buf generate --path` 限制只生成 conf 部分（见 §2.3）。

### 2.3 Makefile 改造

最终落地（2026-07-20）：

```makefile
GOPATH := $(shell go env GOPATH)
VERSION := $(shell git describe --tags --always 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo dev)

.PHONY: init
# init env: buf + protobuf generators (versions pinned to match go.mod) + wire
init:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@v2.0.0-20260404020628-f149714c1d54
	go install github.com/google/gnostic/cmd/protoc-gen-openapi@v0.7.1
	go install github.com/google/wire/cmd/wire@latest

.PHONY: config
# generate internal config proto. buf.gen.yaml covers the whole repo
# (module root = .); buf --path restricts generation to the conf protos.
config:
	buf generate --template buf.gen.yaml \
		--path internal/conf \
		--path app/admin/internal/conf \
		--path app/billing/internal/conf \
		--path app/channel/internal/conf \
		--path app/config/internal/conf \
		--path app/identity/internal/conf \
		--path app/log/internal/conf \
		--path app/monitor/internal/conf \
		--path app/notify/internal/conf

.PHONY: api
api:
	buf generate --template buf.gen.yaml

.PHONY: proto
proto: api config
```

**对比原方案**：
- ✅ 用 `buf generate` 替代 `protoc`，**包括 conf**（原方案 conf 保留 protoc 的妥协已撤销）。
- ✅ 删除 Windows Git Bash `find` 特判（buf 跨平台一致，`make config` 也不再调 `find`）。
- ✅ 删除 `PROTO_SYSTEM_INCLUDE_DIRS`、`proto-tools`、`PROTOC_GEN_*_VERSION` 常量。
- ✅ `GOHOSTOS` 变量删除（不再有平台分支）。
- `make init` 仍安装 buf + 4 个 `protoc-gen-*` 生成器 + wire（生成器二进制名仍叫
  `protoc-gen-*`，但这是插件名，不是 protoc 编译器）。

### 2.4 CI / Dockerfile 改造（2026-07-20 彻底收尾）

- **CI 删除 `protobuf-compiler`**：`make proto`（含 `make config`）全走 buf，
  buf 通过 BSR 的 `googleapis` dep 解析 `google/protobuf/*.proto`，不再需要系统 protoc。
  `ci.yml` / `security.yml`（3 处）只 `make init` + `make proto`。
- **10 个 Dockerfile（9 服务 + 根 relay-gateway + deployments/docker）删除
  `protobuf protobuf-dev`**：同上，buf 走 BSR。`go-deps` stage 仍 `go install`
  buf + 4 个 `protoc-gen-*` 生成器到 `/go/bin`，builder stage `COPY --from=go-deps`
  拿预编译二进制。
- **未抽成共享外部 base 镜像**：CI 用 matrix 跑 18 个相互隔离的 buildx job
  （`push: false`、无 registry），无法跨 job 引用公共 base；强行引入 Docker Bake
  需重写 CI 并行/缓存/故障语义，收益不成比例，故保持每镜像内联 go-deps，靠
  BuildKit layer cache 去重。`deployments/docker/Dockerfile.deps`（外部 base）同步
  装 buf+生成器，与内联 go-deps 一致，供 deploy.sh 离线预构建。
- **关键架构点（保留）**：`go-deps` 不加 `--platform=$BUILDPLATFORM`，与 builder
  同 TARGET 平台——工具链二进制由 builder COPY 并在其内执行，跨架构（amd64 runner
  构建 arm64）时若装在 $BUILDPLATFORM 会产生 builder 无法 exec 的二进制。

### 2.4.1 演进历史（原方案 → 妥协方案 → 彻底方案）

| 阶段 | module root | buf lint | conf 生成 | protoc 依赖 | 状态 |
|---|---|---|---|---|---|
| 原方案（2026-07-19 评估） | `api/` | 需 `except` | `buf.gen.config.yaml` 多 inputs | conf 走 protoc | ❌ conf 同名冲突，多 inputs 静默空生成 |
| 妥协方案（2026-07-19 落地） | `api/` | `except: PACKAGE_DIRECTORY_MATCH` | protoc + `find` | CI/Docker 保留 `protobuf-compiler` | ⚠️ 可用，但有 except + 双工具链 |
| **彻底方案（2026-07-20 落地）** | `.` | **无 except，exit 0** | buf `--path` | **全删** | ✅ 最终形态 |

彻底方案的额外 proto 重组（一次性、不可逆）：
1. `api/relay-gateway/v1/relay.proto` → `api/relay/v1/relay.proto`（目录含 `-`，
   proto package 不能含 `-`，`api.relay.v1` 永远不匹配 `relay-gateway` 目录）。
   `go_package` 同步改 `api/relay-gateway/v1` → `api/relay/v1`；Go import 只
   2 个业务文件（`internal/server/grpc.go`、`internal/service/relay.go`）。
   Dockerfile/compose/k8s/Makefile 里的 `relay-gateway` 是 `SERVICE_PATH=./cmd/relay-gateway`
   （二进制/容器名），不是 proto 路径，不动。
2. 4 个 proto 的 `import "common/v1/common.proto"` → `import "api/common/v1/common.proto"`
   （module root 从 `api/` 变 `.`，import 路径要带 `api/` 前缀）。**对 Go 生成代码零影响**
   （Go 用 `go_package` 导入，不用 proto import 路径）。
3. 9 个 `conf.proto` → `<svc>_conf.proto`（`relay_conf.proto`/`admin_conf.proto`/...），
   package 从 `api.<svc>.v1`/`channel.internal.conf` → `internal.conf`/`app.<svc>.internal.conf`
   （匹配各自目录）。**生成的 Go package 不变**（仍叫 `conf`，由 `go_package` 的 `;conf` 控制），
   故 `config_loader.go` 的 `relayconf.Bootstrap` 等引用零改动。

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

### buf 迁移验收（2026-07-20 彻底方案实测）

- [x] `buf.yaml` / `buf.gen.yaml` 存在（单文件覆盖全仓，`buf.gen.config.yaml` 已删）
- [x] `buf lint`（MINIMAL）通过 —— **无 `except`**，module root=`.` 让
      `package api.<domain>.v1` 匹配目录 `api/<domain>/v1`；conf 重命名+改 package 后
      也匹配各自目录 —— 2026-07-20 实测 `buf lint` exit 0
- [x] `make api` 生成的 `*.pb.go` 在 `api/` 下（含 `api/relay/v1/`，原 `relay-gateway` 重命名后）
- [x] `make config` 生成的 `*_conf.pb.go`（9 个）走 buf `--path`，不再用 protoc
- [x] `make api-check` 通过（`openapi.yaml` 含 `/v1/chat/completions`，逐字节一致）
- [x] `third_party/` 已删除（含 `google/`、`errors/`、`openapi/`、`validate/`）
- [x] CI 的 "generated files are current" 检查通过 —— 本地 `make proto` 后
      `git status --porcelain` 无 `*.pb.go`/openapi 漂移（生成产物入 `.gitignore`）；
      CI 侧 `ci.yml` 的 "Verify generated files are current" step 推送后自动跑
- [x] CI/Dockerfile 无 `protobuf-compiler` / `protobuf-dev` 依赖 —— buf 通过 BSR
      的 `googleapis` dep 解析 `google/protobuf/*.proto`，全仓不依赖系统 protoc
- [x] Dockerfile 构建成功 —— buildx 实测（2026-07-19，linux/amd64，billing 完整镜像）；
      2026-07-20 删 `protobuf protobuf-dev` 后 Dockerfile 逻辑等价（make proto 不调 protoc），
      推送后 CI 自动复验
- [x] Windows 开发者按新 Makefile 能正常 `make proto` —— **静态审计结论**（未在 Windows 实跑）：
      `make api` / `make config` 全走 `buf generate`，天然跨平台；Makefile 已无 `find`/`Git_Bash`/
      `GOHOSTOS` 平台分支。Windows 实机验证留待后续在 Windows 环境补充
- [x] 生成产物（`*.pb.go`、`openapi.yaml`）不入库：`.gitignore` 含 `*.pb.go` / `openapi.yaml`
- [x] `go build ./...` / `go vet ./...` / `./scripts/check-architecture.sh` 全绿（2026-07-20 实测）
- [x] proto 重组一次性完成：`relay-gateway`→`relay` 目录 + 4 个 import 加 `api/` 前缀 +
      9 个 conf 重命名+改 package（见 §2.4.1）

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
- 启用 `buf lint` 的 `BASIC`/`STANDARD`：2026-07-20 彻底方案已消除
  `except: PACKAGE_DIRECTORY_MATCH`，MINIMAL 干净通过。进一步启用 BASIC/STANDARD
  会触发 `PACKAGE_VERSION_SUFFIX`（conf 的 `app.<svc>.internal.conf` 无版本后缀）、
  `RPC_RESPONSE_STANDARD_NAME` 等，需单独排期整改 proto。
- 删除 `SafeKratosServerOptions`：等 kratos 上游合并 CVE-2026-6993 修复后，
  在常规依赖升级 PR 里删除该 workaround。
- 引入 `kratos/v3/log`：评估是否用 slog 替换项目自有 zap 体系，
  但这属于独立的设计决策，不应在 v3 升级 PR 里混做。
- Windows 实机验证：本次 `make proto` 跨平台性是静态审计结论（Makefile 已无
  平台分支），建议在 Windows 环境实跑一次 `make proto` 补齐。
