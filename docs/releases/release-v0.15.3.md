# Micro-One-API v0.15.3 发布：统一 JSON 序列化层（pkg/jsonx）、收尾 gofmt 规范

> 2026-08-06 · 上一版：[v0.15.2](./release-v0.15.2.md)（2026-08-05）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.15.3)

v0.15.3 是 v0.15.2 之后的 **PATCH 版本**（3 个提交），内容为内部重构与代码规范，**无任何对外行为变更**：将全仓库的 JSON 序列化统一收敛到 `pkg/jsonx` 单一封装层（底层 sonic `ConfigStd`，保持 `encoding/json` 语义：HTML 转义、map key 排序、字符串拷贝），消除散落在各模块的 `encoding/json` / 直接 `sonic.*` 调用，使所有序列化站点走同一条 `encoding/json`-兼容链路；并以一次纯机械的 `gofmt -w` 扫描收尾仓库内 50 个不规范 Go 文件的格式债务。

**无 API 破坏性变更、无数据库迁移、无新增配置项、无 proto 变更**。受影响服务为全部后端服务（代码触达面广但均为等价替换）。

## 变更内容

### 1. 引入 pkg/jsonx 统一 JSON 序列化层，迁移全仓库 encoding/json

**背景**：仓库此前的 JSON 序列化存在两条互不一致的路径——业务代码广泛使用
`encoding/json`（标准库语义：HTML 转义、map key 排序、字符串拷贝），而性能敏感
路径偶发直接调用 `github.com/bytedance/sonic` 的包级函数（`sonic.Marshal` /
`sonic.Unmarshal` 使用 `ConfigDefault`，不转义、不排序、不拷贝字符串——解码出的
字符串可能与输入缓冲区别名）。两套语义在边缘场景（含 `<` 的字段、map 迭代顺序、
解码后复用输入缓冲区）会产出不一致结果，且难以全局审计。

**变更**：扩展 `pkg/jsonx` 作为 `encoding/json` 的 drop-in 替代，底层统一为
`sonic.ConfigStd`（保持 `encoding/json` 语义）：

- 提供 `Marshal` / `Unmarshal` / `MarshalIndent` / `Valid` / `NewEncoder` /
  `NewDecoder`，以及 `Number` / `RawMessage` / `Marshaler` / `Unmarshaler` 的
  类型别名（`json.Number` type switch、自定义编解码器继续可用）。
- 将 `app/*`、`internal/*`、`domain/*`、`platform/*` 下的 52 个非测试文件从
  `encoding/json` 迁移到 `jsonx`。
- 升级 `github.com/bytedance/sonic` 至 `v1.15.2`。
- **唯一保留**：`platform/middleware/bodylimit.go` 仍用 `encoding/json`——它依赖
  `DisallowUnknownFields` 与 `*json.SyntaxError` / `*json.UnmarshalTypeError`
  类型断言（sonic 未暴露），在进入业务 handler 前失败关闭。

**影响**：JSON 序列化语义在全仓库收敛为单一、可审计的一层。无对外行为变化。

### 2. 收敛直接 sonic.* 调用，补齐 jsonx.Get 与 JSON 策略文档

**背景**：第一步只迁移了 `encoding/json` 站点，但性能热点路径（上游 provider
适配、adaptor、事件总线、幂等中间件等 53 个文件）仍在直接调用
`sonic.Marshal` / `sonic.Unmarshal` / `sonic.ConfigStd.NewEncoder` /
`sonic.Get`，绕过了封装层，与第一步的目标相悖。

**变更**：

- 将 `internal/server`、`internal/apicompat`、`internal/adaptor`、`internal/biz`、
  `internal/identity`、`domain/upstream/provider/{anthropic,azure,gemini,provider,voyageai}`、
  `app/{channel,config,log,monitor,notify}`、`platform/{events,middleware/idempotency}`
  共 53 个文件中的 `sonic.*` / `sonic.ConfigStd.*` 替换为 `jsonx.*`。
- `pkg/jsonx` 新增 `Get()`（封装 `sonic.Get`，热路径字段查找），并注明它是
  sonic 专有（返回 `ast.Node`、使用 `ConfigDefault`）**不属于**
  `encoding/json`-parity 契约的一部分。
- `AGENTS.md` 新增「JSON serialization」策略章节：业务代码必须用 `jsonx`；
  非测试代码禁止 `import encoding/json`（除 `pkg/jsonx` 自身与 `bodylimit.go`）；
  禁止直接调用 `sonic.*` 包级函数；并记录 sonic 的 `compat.go` 版本边界
  （`go1.27+` / 非 amd64·arm64 架构会回退到 `encoding/json`，需保持 go.mod ≤ 1.26）。
- 新增 `pkg/jsonx/json_test.go`：覆盖与 `encoding/json` 的一致性（HTML 转义、map
  key 排序、float/int 精度、自定义 marshaler）、错误行为、`Get` 路径查找，以及
  sonic-vs-std 基准。

**影响**：所有序列化站点统一走 `jsonx`。无对外行为变化。

### 3. 收尾 gofmt 格式债务

**背景**：前两步重构是等价替换，刻意不在 touched 文件里顺带做 gofmt
重排（留作独立 style 提交，便于 review）。仓库此前另有约 50 个文件存在格式
债务（import 分组、结构体字面量字段对齐、闭包体缩进、单行函数展开等）。

**变更**：一次纯机械的 `gofmt -w` 扫描，无任何语义改动：

- import 分组排序（`config_loader.go` 家族：`platform/registry` vs `platform/config`）
- 结构体字面量字段对齐（`log.go`、`billing` 测试等）
- 闭包体重新缩进（`billing.go`：214 行 `RunInTx` 块）
- 单行函数展开为多行（`subscription_m10_test.go` 的 fake）
- 多余空行清理

**影响**：`gofmt -l` 全仓库 clean。无对外行为变化。

## 兼容性说明

- **API**：无破坏性变更。无 proto 变更。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项。
- **依赖**：`github.com/bytedance/sonic` `v1.15.2`（升级），`go.mod` 维持 go 1.26（sonic `compat.go` 版本边界内）。
- **运行时**：JSON 输出语义与 `encoding/json` 完全一致（已由测试覆盖）；此前直接使用 `sonic.ConfigDefault` 的热点路径现在固定为 `ConfigStd` 语义，边缘场景（HTML 转义、map 顺序、字符串别名）反而更安全、更可预测。
- **CI**：无变更。

## 升级步骤

```bash
git fetch --tags
git checkout v0.15.3

# 纯重构版本，无 proto / 配置 / 迁移变更，无需重新部署即可获得收益；
# 若要随常规发布节奏一并上线，按需交叉构建受影响服务即可：
./scripts/deploy-update.sh <service>
```

## 验证

- `gofmt -l` 全仓库无输出（clean）。
- `go build ./...` 通过。
- 非网络包单元测试通过。
- `pkg/jsonx/json_test.go` 验证与 `encoding/json` 一致性（HTML 转义、map key 排序、数值精度、自定义编解码器）、错误行为、`Get` 路径查找。

## 完整变更日志

- refactor(json): replace encoding/json with sonic via pkg/jsonx wrapper
- refactor(jsonx): route all sonic calls through pkg/jsonx wrapper
- style(gofmt): format 50 noncompliant Go files across the repo
