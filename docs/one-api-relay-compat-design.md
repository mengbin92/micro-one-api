# One-API Relay API 兼容第一期设计

## 背景

当前 `micro-one-api` 已实现 OpenAI 兼容主链路的 `/v1/chat/completions` 和 `/v1/models`，但与同级目录下的 `one-api` 相比，Relay API 面仍明显不足。

`one-api` 的 `router/relay.go` 中除聊天补全外，还暴露以下可代理接口：

- `/v1/completions`
- `/v1/embeddings`
- `/v1/images/generations`
- `/v1/audio/transcriptions`
- `/v1/audio/translations`
- `/v1/audio/speech`
- `/v1/moderations`
- `/v1/oneapi/proxy/:channelid/*target`

第一期目标是补齐这些核心 Relay 路由的兼容能力，让当前微服务网关可以覆盖更多 OpenAI 兼容 API 场景。

## 范围

本期实现：

1. 在 `relay-gateway` 增加上述 REST 路由。
2. 复用现有鉴权、模型映射、渠道选择、重试和账务预扣链路。
3. 为 OpenAI-compatible provider 增加通用 HTTP 转发能力。
4. 对非 chat 接口使用结构化 JSON 透传，减少对不同 API 响应字段的过早建模。
5. 增加单元测试覆盖路由注册、请求转发、鉴权失败、渠道选择失败、账务释放等关键路径。

本期不实现：

1. Web 管理后台。
2. `one-api` 全部 Provider adaptor 矩阵。
3. 文件、fine-tuning、assistants、threads 等在 `one-api` 中也标记为 `RelayNotImplemented` 的接口。
4. 完整 multipart 文件上传语义的深度解析。本期对 audio/image 走通用请求体透传，由 provider 侧保持 Content-Type。

## API 设计

新增路由全部位于 `relay-gateway` HTTP 层：

| 路由 | 方法 | 上游路径 | 说明 |
|---|---|---|---|
| `/v1/completions` | POST | `/completions` | 文本补全 |
| `/v1/embeddings` | POST | `/embeddings` | 向量嵌入 |
| `/v1/images/generations` | POST | `/images/generations` | 绘图 |
| `/v1/audio/transcriptions` | POST | `/audio/transcriptions` | 语音转文字 |
| `/v1/audio/translations` | POST | `/audio/translations` | 语音翻译 |
| `/v1/audio/speech` | POST | `/audio/speech` | 文字转语音 |
| `/v1/moderations` | POST | `/moderations` | 内容审核 |
| `/v1/oneapi/proxy/{channel_id}/...` | ANY | 原样路径 | 指定渠道代理 |

Chat 仍保留现有专用实现。新增路由使用通用 relay handler：

1. 解析 Bearer token。
2. 从请求体或 query 中提取 `model`，缺失时按接口类型使用合理默认：
   - embeddings: `text-embedding-ada-002`
   - moderations: `text-moderation-latest`
   - audio speech: `tts-1`
   - 其他接口要求显式 model。
3. 调用 `RelayUsecase.Plan` 完成鉴权、模型映射和渠道选择。
4. 预扣账务额度。
5. 调用 provider 的通用转发接口。
6. 成功后提交账务，失败后释放账务。

## Provider 设计

新增 `RawRequest` / `RawResponse`：

```go
type RawRequest struct {
    Method      string
    Path        string
    Query       string
    Header      http.Header
    Body        []byte
    ContentType string
}

type RawResponse struct {
    StatusCode int
    Header     http.Header
    Body       []byte
}
```

`Provider` 增加：

```go
Forward(ctx context.Context, req *RawRequest) (*RawResponse, error)
```

OpenAI-compatible provider 将请求转发到 `baseURL + path`，设置上游认证头并过滤 hop-by-hop headers。Anthropic/Gemini 暂时仅支持 chat 专用接口；非 chat 通用路由若选择到这些 provider，返回明确的 unsupported error。

## 账务策略

本期通用接口不尝试完整计算 tokens。策略：

- 请求前按请求体大小和接口类型估算预扣。
- 成功后按响应 usage 字段尽量读取 `total_tokens`；读取不到时提交预扣值。
- 上游错误、provider 不支持、写响应失败前的错误均释放预扣。

这保持账务链路一致性，同时避免为每个 API 单独实现复杂计费。

## 测试策略

1. Provider 单元测试：
   - OpenAI provider 正确拼接路径和 query。
   - 正确设置 `Authorization: Bearer <key>`。
   - 保留 JSON 与 multipart Content-Type。
   - 非 2xx 上游响应作为错误返回。

2. Relay HTTP 单元测试：
   - 新路由注册并只接受预期方法。
   - 缺少 Authorization 返回 401。
   - 缺少 model 的接口按规则默认或返回 400。
   - 上游成功时返回原始响应并提交账务。
   - 上游失败时释放账务。

3. 回归测试：
   - 现有 `/v1/chat/completions` 和 `/v1/models` 测试继续通过。
   - `go test ./...` 通过。

## 验收标准

1. `go test ./...` 通过。
2. `go build ./...` 通过。
3. 本文档保留在 `docs/`。
4. 新增 API 至少覆盖 completions、embeddings、image generation、audio、moderations 的基础代理路径。
5. 未实现的接口返回清晰错误，不出现 silent fallback。
