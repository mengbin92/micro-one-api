# Micro-One-API v0.17.1 发布：修复上游 SSE 流卡死

> 2026-08-10 · 上一版：[v0.17.0](./release-v0.17.0.md)（2026-08-08）· [GitHub Release](https://github.com/mengbin92/micro-one-api/releases/tag/v0.17.1)

v0.17.1 是 v0.17.0 之后的 **PATCH 版本**（3 个提交，v0.17.0 `084191b` → develop HEAD `b0d99cc`），核心是修复上游 SSE 流卡死导致连接泄漏的生产问题：当上游供应商的流式响应在建立连接后停止吐字节且不断开 TCP 时，relay-gateway 侧的读取会无限挂起（原 `streamClient` 无任何超时），直到请求 context 到期才释放，高并发下造成连接数堆积。本版本引入滑动 idle 超时——活跃流（持续有字节）不受影响，仅在连续 `timeout` 无字节时主动断开并记日志。

**无 API 破坏性变更、无数据库迁移、无 proto 变更**。受影响服务为 relay-gateway。

## 变更内容

### 1. fix(relay): bound stalled upstream SSE streams（ff518b1）

**根因**：v0.17.0 及之前，OpenAI / Anthropic / Azure / Gemini 四个 provider 的 `streamClient` 均为 `&http.Client{}`（无 `Client.Timeout`），这是 domain-H3 的有意设计——`http.Client.Timeout` 是覆盖整个 round trip（含 response body 读取）的硬截止，会杀掉仍在正常传数据的 SSE 长连接。但"无超时"也意味着：如果上游接受了请求、返回了 200 + SSE 响应头后停止发送任何字节（既不 `data:` 也不关闭连接），relay-gateway 的 `io.Copy` 会永久阻塞在 `Body.Read()` 上，仅靠请求 context deadline 兜底。当 context deadline 很长（长流式对话场景）且上游频繁出现 stall 时，连接数堆积可触发 file descriptor / 内存压力。

**修复**：新增 `domain/upstream/provider/stream_timeout.go`，提供 `newStreamHTTPClient(timeout)` 替代裸 `&http.Client{}`：

- **响应头等待有界**：底层 `http.Transport.ResponseHeaderTimeout = timeout`，上游迟迟不返回响应头时在 `timeout` 后失败（而非无限等）。
- **滑动 idle 超时**：`streamTimeoutRoundTripper` 在成功拿到响应后，用 `streamIdleReadCloser` 包裹 `resp.Body`。该 wrapper 启动一个 goroutine 监听读活动：每当 `Read` 返回 `n > 0`（有字节到达），就重置 idle 计时器；连续 `timeout` 内无任何字节到达时，主动 `Close()` 底层 body 并标记 `ErrStreamIdleTimeout`。活跃流（持续有 token 产出）的 idle 计时器不断重置，不会被误杀。
- **关键设计**：不使用 `http.Client.Timeout`（硬总截止），而是 per-read 的滑动 idle——正常的长流式响应（如几分钟的 reasoning 输出）只要在持续产 token，就不会被断开；只有真正 stall 的流才被回收。
- **资源安全**：Transport 按 timeout 值缓存复用（`sync.Map` + `LoadOrStore`），`streamIdleReadCloser` 的 `Close`/`done` 用 `sync.Once` 保证 goroutine 不泄漏；timer 重置遵循 `Stop` + drain channel 标准模式。
- **可观测**：`internal/server/http_raw_helpers.go` 的 `writeRawStreamResponse` 在 `io.Copy` 返回 `ErrStreamIdleTimeout` 时输出 warn 日志（`upstream SSE stream closed after idle timeout`），运维可据此统计 stall 频率。

**测试**：`domain/upstream/provider/raw_test.go` 新增 110 行测试，覆盖 idle 超时触发断开、活跃长流不被杀、timer 重置竞态等场景。

**影响**：relay-gateway（OpenAI / Anthropic / Azure / Gemini provider 的流式路径）。非流式路径（`httpClient`）不受影响。无配置项变更，timeout 复用 provider 已有的 `timeout` 配置值。

### 2. chore(p3): complete P3.1 amd64 baseline + P3.2 jsonx final decision（5882f7b）

**内容**：P3 性能基线工作的收尾文档与基准数据归档，**无任何代码逻辑变更**。

- **P3.1（Linux/amd64 三版本基线复测）**：在部署环境（x86_64）对 v0.16.0（`a8e14db`）和 develop（`ff518b1`）各跑 3 轮 k6 全负载（billing 表 truncate），100% 成功、0 dropped；chat P95 116.68ms（v0.16.0）vs 116.34ms（develop），无回退（±1% 噪声内）。修复了 08-09 首跑失败（`SERVICE_TOKEN` 缺失导致 `ConsumeTokenQuota` PermissionDenied）。`BASELINE.md` 填充完成，执行报告归档。
- **P3.2（jsonx 最终决策）**：Linux/amd64 上 sonic 在 Marshal 和 Unmarshal 两个方向均胜出 std（Unmarshal 大负载 4.7x、小负载 3.5x；Marshal 大负载 3.0x），与 arm64 的 Marshal 方向结论相反。最终决策：**保留 `pkg/jsonx` 单一封装层，任何方向都不回退到 `encoding/json`**。决策文档更新至 §3.0，CPU profile 归档。
- **文档**：`docs/TODO.md` P1/P3 状态置 done、`docs/design/v0.17-roadmap.md` 状态更新、`.gitignore` 排除 k6 原始采样（`results/**/raw-*.json`）。

**影响**：纯文档 + 基准数据，无运行时行为变化。

### 3. fix(docs): correct P31 execution report link path（b0d99cc）

**内容**：`docs/TODO.md` 第 278、888 行引用 P31 执行报告的相对路径多写了一层 `../`（`../../scripts/...` 应为 `../scripts/...`），导致 `scripts/check-markdown-links.py` CI 检查失败。

**影响**：CI 修复，无代码变更。

## 兼容性说明

- **API**：无破坏性变更。无 proto 变更。
- **数据库**：无新增迁移文件。
- **配置**：无新增配置项（idle 超时复用 provider 已有 `timeout` 配置）。
- **运行时**：relay-gateway 流式路径新增 idle 超时保护。此前依赖请求 context deadline 兜底的 stall 场景，现在会在 `timeout` 内主动断开。正常流式响应不受影响。

## 升级步骤

```bash
git fetch --tags
git checkout v0.17.1

# 无数据库迁移；本版本仅影响 relay-gateway，重新构建该服务镜像即可：
./scripts/deploy-update.sh relay-gateway
```

## 验证

- `go build ./...`：编译通过。
- `go test ./domain/upstream/provider/... -v -count=1`：全部通过（含新增 idle 超时 / 活跃长流保活测试）。
- `python3 scripts/check-markdown-links.py`：98 文件检查通过。
- **CI run 31362121791**：22 个 job（4 基础 + 18 docker 构建覆盖 9 服务 × {linux/amd64, linux/arm64}）全部 success。

## 完整变更日志

- fix(relay): bound stalled upstream SSE streams
- chore(p3): complete P3.1 amd64 baseline + P3.2 jsonx final decision
- fix(docs): correct P31 execution report link path in TODO.md (../../ -> ../)
