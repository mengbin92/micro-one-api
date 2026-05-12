# One API Remaining Gap Priority List

> Branch: `docs/one-api-gap-refresh-20260512`
> Date: 2026-05-12
> Source: current `develop` code after One API gap phases 1-3, `docs/one-api-full-gap-analysis-20260509.md`, and sibling `../one-api`.

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

## Recommended Execution Order

1. Build or migrate the full web frontend against the current `/api/*` compatibility layer.
2. Complete OAuth/OIDC/Lark/WeChat, bind flows, Turnstile, and registration email-domain restrictions.
3. Add channel balance refresh adapters and persistence.
4. Add dashboard billing subscription compatibility.
5. Add stable NotImplemented compatibility routes for the remaining OpenAI route surface.
6. Implement real log deletion, group management, and content management only when required by the frontend.
7. Add provider-native adapters in demand order, starting with Azure details and the highest-traffic non-OpenAI-compatible channels.

## Documentation Policy

Completed one-off design and implementation plan documents should not remain as active planning artifacts. This file is the current priority source for remaining One API gaps. Historical architecture, deployment, security, and broad gap-analysis documents remain as reference material.
