# One-API Relay API 兼容第一期实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:test-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 `one-api` 核心 Relay API 面，让当前 `relay-gateway` 支持 completions、embeddings、images、audio、moderations 和指定渠道 proxy 的基础转发。

**Architecture:** 在 HTTP server 增加通用 relay handler，复用现有 `RelayUsecase.Plan` 做鉴权、模型映射、渠道选择和重试。Provider 层新增原始 HTTP 转发接口，OpenAI-compatible provider 先支持通用转发，Anthropic/Gemini 对非 chat 返回 unsupported。

**Tech Stack:** Go, go-kratos HTTP server, standard `net/http`, existing relay provider and billing clients, `go test`.

---

## File Map

- Modify `internal/relay/provider/provider.go`: 新增 `RawRequest`、`RawResponse` 和 `Forward` 接口，给 OpenAI provider 实现通用转发。
- Modify `internal/relay/provider/anthropic.go`: 增加 unsupported `Forward` 实现。
- Modify `internal/relay/provider/gemini.go`: 增加 unsupported `Forward` 实现。
- Create `internal/relay/provider/raw_test.go`: 覆盖 OpenAI provider 通用转发行为。
- Modify `internal/relay/server/http.go`: 注册新增路由，实现通用 handler、模型提取、账务提交/释放和响应透传。
- Create or extend `internal/relay/server/http_raw_test.go`: 覆盖新增 HTTP 路由行为。
- Modify `docs/one-api-relay-compat-design.md`: 若实现中有合理差异，更新设计说明。

## Task 1: Provider Raw Forwarding

- [ ] Step 1: Write failing provider tests in `internal/relay/provider/raw_test.go`.

Cover:

- `Forward` sends request to `<baseURL>/<path>?<query>`.
- `Forward` overwrites upstream `Authorization` with provider key.
- `Forward` preserves `Content-Type`.
- `Forward` returns body/status/headers for 2xx responses.
- `Forward` returns error for non-2xx responses.

Run:

```bash
go test ./internal/relay/provider -run 'TestOpenAIProviderForward' -count=1
```

Expected: FAIL because `Forward` is not implemented.

- [ ] Step 2: Implement raw request/response types and OpenAI `Forward`.

Key requirements:

- Use `http.NewRequestWithContext`.
- Join base URL and path without double slash bugs.
- Copy request headers except hop-by-hop headers and caller Authorization.
- Set provider Authorization.
- Read full response body.

- [ ] Step 3: Add unsupported `Forward` methods for Anthropic/Gemini.

Return a clear error such as `raw forwarding is not supported by anthropic provider`.

- [ ] Step 4: Run provider tests.

```bash
go test ./internal/relay/provider -count=1
```

Expected: PASS.

## Task 2: HTTP Route Registration and Request Mapping

- [ ] Step 1: Write failing HTTP server route tests in `internal/relay/server/http_raw_test.go`.

Cover:

- `POST /v1/completions` reaches raw handler.
- `POST /v1/embeddings` reaches raw handler.
- `POST /v1/images/generations` reaches raw handler.
- `POST /v1/audio/transcriptions` reaches raw handler.
- `POST /v1/audio/translations` reaches raw handler.
- `POST /v1/audio/speech` reaches raw handler.
- `POST /v1/moderations` reaches raw handler.
- Missing Authorization returns 401.

Run:

```bash
go test ./internal/relay/server -run 'TestHTTPServerRawRoutes' -count=1
```

Expected: FAIL because routes do not exist.

- [ ] Step 2: Register routes in `HTTPServer.RegisterRoutes`.

Add exact route handlers:

- `/v1/completions`
- `/v1/embeddings`
- `/v1/images/generations`
- `/v1/audio/transcriptions`
- `/v1/audio/translations`
- `/v1/audio/speech`
- `/v1/moderations`

- [ ] Step 3: Implement method validation and Authorization parsing through existing helper style.

Keep behavior aligned with existing `/v1/chat/completions`.

- [ ] Step 4: Run HTTP route tests.

```bash
go test ./internal/relay/server -run 'TestHTTPServerRawRoutes' -count=1
```

Expected: PASS.

## Task 3: Raw Relay Execution

- [ ] Step 1: Write failing tests for successful raw relay and upstream failure.

Cover:

- Request with `model` selects channel and forwards body.
- Response status/body/content-type are returned to client.
- Successful response commits billing.
- Upstream error releases billing.
- Missing required model returns 400 where no default model is defined.
- Embeddings/moderations/audio speech use default models when omitted.

Run:

```bash
go test ./internal/relay/server -run 'TestHTTPServerRawRelay' -count=1
```

Expected: FAIL because raw execution is missing.

- [ ] Step 2: Implement model extraction.

For JSON requests, parse only top-level `model` using `map[string]any` or `sonic.Get`. Do not destructively rewrite the request body unless model mapping requires it.

Default model rules:

- `/v1/embeddings`: `text-embedding-ada-002`
- `/v1/moderations`: `text-moderation-latest`
- `/v1/audio/speech`: `tts-1`

- [ ] Step 3: Implement raw forwarding with retry.

Use existing retry executor and `RelayUsecase.Plan`. For each selected channel:

1. Reserve quota.
2. Create provider.
3. Call `provider.Forward`.
4. Commit quota on 2xx.
5. Release quota on error.

- [ ] Step 4: Write response passthrough.

Copy selected response headers, status code, and body. Avoid hop-by-hop headers.

- [ ] Step 5: Run raw relay tests.

```bash
go test ./internal/relay/server -run 'TestHTTPServerRawRelay' -count=1
```

Expected: PASS.

## Task 4: OneAPI Proxy Route

- [ ] Step 1: Write failing test for `/v1/oneapi/proxy/{channel_id}/...`.

Cover:

- Explicit channel id path forwards to target path.
- Missing or invalid channel id returns 400.
- Method is preserved.

Run:

```bash
go test ./internal/relay/server -run 'TestHTTPServerOneAPIProxy' -count=1
```

Expected: FAIL because proxy route is missing.

- [ ] Step 2: Implement proxy route handler.

Because Kratos path parameters are not currently used in this server, parse the path prefix manually:

- Prefix: `/v1/oneapi/proxy/`
- Segment after prefix: channel id
- Remaining path: upstream target path

Use `channelClient.GetChannel` directly for explicit channel selection, while still validating token through identity service.

- [ ] Step 3: Run proxy tests.

```bash
go test ./internal/relay/server -run 'TestHTTPServerOneAPIProxy' -count=1
```

Expected: PASS.

## Task 5: Verification and Documentation

- [ ] Step 1: Run focused tests.

```bash
go test ./internal/relay/provider ./internal/relay/server -count=1
```

Expected: PASS.

- [ ] Step 2: Run full test suite.

```bash
go test ./...
```

Expected: PASS.

- [ ] Step 3: Run build.

```bash
go build ./...
```

Expected: PASS.

- [ ] Step 4: Update docs if implementation differs.

Files:

- `docs/one-api-relay-compat-design.md`
- `docs/one-api-relay-compat-plan.md`

- [ ] Step 5: Check git status.

```bash
git status --short --branch
```

Expected: only intended docs, relay provider, and relay server files changed.
