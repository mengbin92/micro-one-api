# One API Remaining Gap Priority List

> Branch: `docs/one-api-gap-refresh-20260512`
> Date: 2026-05-12
> Source: current `develop` code after One API gap phases 1-3 and sibling `../one-api`.

## Summary

The project now covers the core microservice skeleton, OpenAI-compatible relay path, token validation, channel selection, billing reservation/commit/release flow, structured usage logs, user dashboard aggregation, expanded token/channel/option fields, and common One API-compatible admin/user routes.

It is still not a full One API product. The largest remaining gaps are the full web experience, complete OAuth/SSO and anti-abuse flows, provider-specific channel balance refresh, deeper relay route parity, dashboard subscription compatibility, provider-native adapters, and a few management semantics that require real downstream service support.

## Recently Completed

These items from the earlier priority list are now implemented or mostly implemented:

| Area | Current State |
| --- | --- |
| Business usage logs | Relay success paths write One API-style usage fields into `log-service`: model, token name, quota, prompt/completion tokens, channel ID, elapsed time, stream flag, username, and request metadata. |
| User dashboard aggregation | `log-service` aggregates usage by day and model; authenticated dashboard endpoints expose usage data. |
| Dashboard billing usage | `/dashboard/billing/usage` and `/v1/dashboard/billing/usage` exist and return OpenAI dashboard-style usage totals. |
| Token route and field parity | `/api/token` routes support list, search, path ID, body-ID update, delete, and One API fields such as `accessed_time`, `used_quota`, `subnet`, `unlimited_quota`, quota, expiration, and exhausted status. |
| Channel field parity | Channel persistence and responses include weight, test time, response time, balance, balance updated time, used quota, model mapping, and system prompt. |
| System option key parity | `/api/option/` exposes a broader One API option set for auth, registration, SMTP, Turnstile, ratios, themes, notices, links, retry, and display flags. |
| Common admin/user routes | Admin/user compatibility aliases now cover users, channels, logs, tokens, redemptions, top-up, channel tests, options, user self-service, invitation, email bind, content, groups, and status. |

## Priority 0: Product Usability

These gaps still block a One API-like product experience.

| Area | Current State | Needed Work |
| --- | --- | --- |
| Web frontend | Only a lightweight embedded admin HTML exists. | Build or migrate a real user/admin frontend covering login, user self-service, tokens, channels, redemptions, logs, settings, dashboard charts, content, groups, and OAuth/bind flows. |
| OAuth/SSO and anti-abuse UX | GitHub/Google and generic `/v1/oauth/*` exist; `/api/oauth/email/bind`, reset-password placeholders, and verification endpoints exist. | Add One API-compatible OIDC, Lark, WeChat, OAuth state route, provider bind flows, Turnstile enforcement, and registration email-domain whitelist behavior. |
| Channel balance refresh | `/api/channel/update_balance` and `/api/channel/update_balance/{id}` return stable NotImplemented responses. | Implement provider-specific balance adapters, persist balance and update time, and define failure/disable semantics. |

## Priority 1: Compatibility Depth

These gaps affect frontend compatibility and operational behavior.

| Area | Current State | Needed Work |
| --- | --- | --- |
| Dashboard billing subscription | Usage endpoints exist; subscription endpoint is still missing. | Add `/dashboard/billing/subscription` and `/v1/dashboard/billing/subscription` with stable OpenAI dashboard-style response data. |
| OpenAI route surface | Chat, completions, embeddings, images generation, audio, moderation, models, model details, and proxy are registered. | Add compatibility routes for edits, engines embeddings, files, fine-tuning, assistants, and threads. Unsupported routes can initially return stable NotImplemented responses. |
| Log management semantics | User/admin log list, search, and stats exist; admin delete history currently returns NotImplemented through the compatibility layer. | Add safe historical log deletion only after log/billing storage exposes explicit delete semantics and audit boundaries. |
| Group management | `/api/group` exposes basic group/model data. | Add full group configuration management API if the web frontend needs editable group settings. |
| Content management | `/api/notice`, `/api/about`, and `/api/home_page_content` expose content values. | Add authenticated management endpoints and frontend editing workflow if content administration is required. |

## Priority 2: Provider and Relay Depth

These gaps affect upstream provider coverage.

| Area | Current State | Needed Work |
| --- | --- | --- |
| Azure/OpenAI-compatible details | Azure is recognized as an OpenAI-compatible provider that requires a base URL. | Add Azure API-version/deployment handling, endpoint defaults, and validation that matches One API channel behavior. |
| Provider-native adapters | Anthropic and Gemini have dedicated adapters; many providers use generic OpenAI-compatible forwarding. | Add adapters based on actual channel demand: Baidu, Ali, Xunfei, Tencent, Zhipu, Volcano/Doubao, Ollama, Replicate, Cloudflare, VertexAI, OpenRouter, SiliconFlow, and others. |
| Provider model defaults | `/api/models` and `/api/channel/models` provide basic data from current config/channels. | Expand provider default base URLs, model lists, and metadata where the frontend expects One API's built-in provider catalog. |

### 2026-05-17 Refresh

Backend compatibility items in Priority 1 have been mostly closed by follow-up changes: OAuth/OIDC/Lark/WeChat and bind flows, Turnstile and email-domain registration checks, channel balance refresh for supported providers, dashboard billing subscription routes, stable NotImplemented OpenAI route surface, safe log deletion, group management, and content management now have route-level support and tests.

Provider catalog parity is partially improved: `/api/models` keeps the legacy One API channel-type-to-model-list response and now also returns provider metadata including provider name, default base URL, required config fields, adapter state, and OpenAI-compatible/native support flags. The catalog explicitly marks providers that still require native adapters so unsupported types do not appear as silently supported relay paths.

Still out of scope for the current backend gap pass:
- Full web frontend remains deferred.
- Native adapters are still needed for Cloudflare, VertexAI, Replicate, Baidu, Xunfei, Tencent Hunyuan, and other provider-specific protocols.
- Provider model lists are intentionally conservative defaults and should be expanded with real provider demand.

## Completion Plan

This is the concrete follow-up plan for the remaining gaps. Each item should land as a small branch with route-level tests first, then implementation.

### 1. Web Frontend

Goal: replace the embedded admin HTML with a usable One API-style product UI.

Scope:
- Add login/logout and session/token handling against `/api/user/login`, `/api/user/logout`, and `/api/user/self`.
- Add user pages for dashboard charts, token CRUD, top-up, invitation code, and available models.
- Add admin pages for users, channels, redemptions, logs, options, status, content, and groups.
- Keep the frontend aligned to existing `/api/*` response shapes before introducing new backend routes.

Acceptance:
- A user can register/login, create and manage tokens, view dashboard usage, redeem quota, and manage their profile.
- An admin can manage users, channels, redemptions, logs, and options from the UI.
- Browser smoke tests cover the primary user and admin workflows.

### 2. OAuth, Bind Flows, and Anti-Abuse

Goal: make login, registration, and account binding match One API's expected user experience.

Scope:
- Add `/api/oauth/state`.
- Add One API-compatible `/api/oauth/oidc`, `/api/oauth/lark`, `/api/oauth/wechat`, and `/api/oauth/wechat/bind`.
- Extend identity OAuth provider configuration for OIDC, Lark, and WeChat.
- Enforce Turnstile checks on registration/reset flows when enabled by options.
- Enforce registration email-domain whitelist when enabled by options.

Acceptance:
- Each OAuth provider has authorize/callback tests and disabled-provider tests.
- Bind endpoints require an authenticated user and reject duplicate provider identities.
- Registration tests cover Turnstile enabled/disabled and email-domain allow/deny cases.

### 3. Channel Balance Refresh

Goal: turn `/api/channel/update_balance` from a stable NotImplemented placeholder into real balance refresh.

Scope:
- Define a balance adapter interface in the channel/admin boundary.
- Implement initial adapters for OpenAI-compatible dashboard billing, OpenRouter credits, SiliconFlow user info, and DeepSeek balance.
- Persist refreshed `balance` and `balance_updated_time`.
- Keep unsupported providers explicit and non-fatal.

Acceptance:
- `/api/channel/update_balance/{id}` refreshes supported channel types and returns balance data.
- `/api/channel/update_balance` refreshes all supported enabled channels and reports per-channel results.
- Tests cover success, unsupported provider, upstream failure, and persistence.

### 4. Dashboard Billing Subscription

Goal: complete OpenAI dashboard-style billing compatibility.

Scope:
- Add `/dashboard/billing/subscription` and `/v1/dashboard/billing/subscription`.
- Return stable fields expected by dashboard-style clients, even if backed by account defaults.
- Keep error shape consistent with existing dashboard billing usage endpoint.

Acceptance:
- Authenticated requests return a stable subscription object.
- Missing auth returns the existing dashboard-style error shape.
- Tests cover both route prefixes.

### 5. Remaining OpenAI Route Surface

Goal: make unsupported OpenAI-compatible routes fail predictably instead of 404.

Scope:
- Add routes for `/v1/edits`, `/v1/engines/*/embeddings`, `/v1/files`, `/v1/fine_tuning/jobs`, `/v1/assistants`, `/v1/threads`, and related path operations.
- Initially return stable NotImplemented OpenAI error payloads.
- Promote individual routes from NotImplemented to proxy/native support only when a provider and data model exist.

Acceptance:
- Each route has a test proving method, path, status code, and error shape.
- Existing chat/completions/embeddings/images/audio/moderation routes are unchanged.

### 6. Management Semantics

Goal: finish lower-level admin semantics after the frontend confirms they are needed.

Scope:
- Add safe historical log deletion only after log storage exposes explicit delete operations and audit constraints.
- Add group configuration management if editable groups are required by the UI.
- Add authenticated content management for notice, about, and home page content.

Acceptance:
- Deletion operations are scoped, audited, and tested against accidental broad deletes.
- Group/content writes have admin auth tests and persistence tests.

### 7. Provider-Native Adapters

Goal: improve quality and reliability for non-OpenAI-compatible upstreams.

Scope:
- Start with Azure API-version/deployment behavior.
- Add native adapters in demand order for Baidu, Ali, Xunfei, Tencent, Zhipu, Volcano/Doubao, Ollama, Replicate, Cloudflare, VertexAI, OpenRouter, and SiliconFlow.
- For each adapter, cover request conversion, response conversion, streaming, usage extraction, and error mapping.

Acceptance:
- Each adapter has non-streaming, streaming, usage, and upstream-error tests.
- Provider defaults include base URL, supported models, and required channel config fields.

## Recommended Execution Order

1. Build or migrate the full web frontend against the current `/api/*` compatibility layer.
2. Complete OAuth/OIDC/Lark/WeChat, bind flows, Turnstile, and registration email-domain restrictions.
3. Add channel balance refresh adapters and persistence.
4. Add dashboard billing subscription compatibility.
5. Add stable NotImplemented compatibility routes for the remaining OpenAI route surface.
6. Implement real log deletion, group management, and content management only when required by the frontend.
7. Add provider-native adapters in demand order, starting with Azure details and the highest-traffic non-OpenAI-compatible channels.

## Documentation Policy

Completed one-off design and implementation plan documents should not remain as active planning artifacts. This file is the current priority source for remaining One API gaps. Architecture and deployment documents remain as reference material.
