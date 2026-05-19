# One API Remaining Gap Priority List

> Branch: `docs/one-api-gap-refresh-20260512`
> Date: 2026-05-12 (main table refreshed 2026-05-19; topup/affiliate/pay clarified 2026-05-19; balance-refresh semantics shipped 2026-05-19; reconciliation review surface shipped 2026-05-20; provider-native adapters paused 2026-05-20)
> Source: current `develop` code and sibling `../one-api`.

## Summary

The project now covers the core microservice skeleton, OpenAI-compatible relay path, token validation, channel selection, billing reservation/commit/release flow, structured usage logs, user dashboard aggregation (usage + subscription), expanded token/channel/option fields, OAuth/SSO and bind flows for GitHub/Google/OIDC/Lark/WeChat/Telegram with Turnstile and email-domain enforcement, channel balance refresh with explicit stay-enabled-but-stale semantics for unsupported providers and audit-visible tracking columns plus opt-in auto-disable on persistent failure for supported providers, group and content management, a wide NotImplemented-stable OpenAI route surface, redemption-code top-up (`/api/user/topup`) and admin quota grant (`/api/topup`) with ledger writes, registration-time invitation bonus credit through the billing service, and a persisted reconciliation run history with admin review endpoints.

It is still not a full One API product. The largest remaining gap is the full web frontend. Provider-native adapters for the eight non-OpenAI-compatible upstreams (Hunyuan, Xingchen, Bedrock, Cloudflare, VertexAI, Replicate, Baidu, Xunfei) are **paused** pending sandbox credentials or a staging deployment — see "Provider-Native Adapters — Paused" below. Online payment and a standalone affiliate-transfer endpoint are intentionally out of scope: upstream one-api does not implement either in its backend (its Air theme frontend calls `/api/user/pay` / `/api/user/amount` but the routes are never registered server-side, and there is no `aff_transfer` endpoint or `aff_quota` field upstream).

## Recently Completed

These items from earlier priority lists are now implemented:

### Since 2026-05-19

| Area | Current State |
| --- | --- |
| Top-up — user redemption | `/api/user/topup` accepts `{key}`, delegates to `billing.RedeemCode`, which validates the code, credits the user via `accountRepo.UpdateQuota`, writes a `Ledger` entry (`LedgerTypeRedeem`), and records a `RedeemRecord`. End-to-end wired since the billing service was added; earlier "P0 top-up workflow" entry was stale. |
| Top-up — admin grant | `/api/topup` (admin) calls `billing.TopUpQuota(user_id, amount, operator_id, remark)`; same ledger path with `LedgerTypeRecharge`. |
| Invitation bonus ledger | Registration-time inviter/invitee credit (gated by `INVITER_BONUS_QUOTA` / `INVITEE_BONUS_QUOTA`) now routes through `billingClient.TopUpQuota` from the identity HTTP layer, producing audit-visible ledger rows instead of bypassing the billing service. Identity biz no longer mutates `users.quota` for affiliate credits. |
| Online payment placeholder shape | `/api/user/pay` and `/api/user/amount` now return the canonical `{success:false, message:"online payment is not configured"}` shape instead of the ad-hoc `{success:false, message:"disabled", data:"..."}` shape. Routes remain intentionally disabled. |
| Balance refresh — failure semantics | Unsupported providers now return `success=true, skipped=true` with a clarifying message instead of an error; the channel is left enabled with whatever stale balance it had. Supported providers persist a `balance_refresh_last_error`, `balance_refresh_last_success_time`, and `consecutive_balance_refresh_failures` per attempt (new columns added in migration `020_add_channel_balance_refresh_tracking.sql`). When `AutomaticDisableChannelEnabled=true` AND `ChannelDisableThreshold > 0`, persistent failures that reach the threshold flip the channel status to disabled. Default options (`false` / `0`) preserve current behavior. |
| Balance persistence bug fix | `Repository.updateChannelDB`'s Updates map previously omitted `balance` and `balance_updated_time`, so admin-triggered refreshes silently dropped persistence outside of the in-memory test repo. Map now includes all balance + tracking columns. |

### Since 2026-05-20

| Area | Current State |
| --- | --- |
| Reconciliation review surface | Reconciliation runs are now persisted via migration `021_create_reconciliation_runs.sql` (run history + JSON-encoded discrepancies). New billing RPCs `ListReconciliationRuns` / `GetReconciliationRun` expose the history, and the admin service surfaces them at `GET /api/reconciliation` (paginated list) and `GET /api/reconciliation/{id}` (drill-down with discrepancies), both gated by `AdminAuth`. The existing `/v1/reconciliation` endpoint on billing-service still triggers an immediate run; that result is now also persisted. |

### Since 2026-05-12

| Area | Current State |
| --- | --- |
| OAuth/SSO and anti-abuse | GitHub/Google/OIDC/Lark/WeChat/Telegram login + bind, `/api/oauth/state`, Turnstile verification, email-domain whitelist all wired up; one user can hold multiple OAuth identities. |
| Channel balance refresh | `/api/channel/update_balance` and `/api/channel/update_balance/{id}` refresh OpenAI, DeepSeek, OpenRouter, SiliconFlow channels via `balanceAdapterForChannel`; result persisted to `balance` and `balance_updated_time`. |
| Dashboard billing subscription | `/dashboard/billing/subscription` and `/v1/dashboard/billing/subscription` return stable subscription objects. |
| OpenAI route surface | edits, engines embeddings, files, fine_tuning (incl. graders), assistants, threads, batches, images edits/variations, audio, moderations, vector, eval, containers — all return stable NotImplemented OpenAI error payloads. |
| Group management | `/api/group` supports GET/POST/PUT/DELETE with optional `with_ratio`. |
| Content management | `/api/notice`, `/api/about`, `/api/home_page_content` accept GET and authenticated PUT. |
| Log deletion | `log-service` exposes `DeleteLogs` with mandatory `end_time`; admin-api proxies via `/api/log` DELETE when `LOG_HTTP_ENDPOINT` + `SERVICE_TOKEN` are configured. |
| Azure provider details | `azure.go` accepts `APIVersion` config; factory rejects empty `base_url`. |
| Provider catalog metadata | `/api/models` returns provider name, default base URL, required config fields, adapter state, and OpenAI-compatible/native flags; native-only providers are explicitly marked. |

## Priority 0: Product Usability

| Area | Current State | Needed Work |
| --- | --- | --- |
| Web frontend | Single embedded `admin.html` (~747 lines) only. | Build or migrate a real user/admin frontend covering login, user self-service, tokens, channels, redemptions, logs, settings, dashboard charts, content, groups, and OAuth/bind flows. |

## Priority 1: Compatibility Depth

| Area | Current State | Needed Work |
| --- | --- | --- |
| Log deletion deployment dependency | Admin-api log delete depends on `LOG_HTTP_ENDPOINT` + `SERVICE_TOKEN`; missing env returns NotImplemented at runtime. | Surface this prerequisite in `deployment.md` and add a config-validation warning on admin-api startup. |
| Provider model defaults | Catalog metadata is stable; per-provider model lists are conservative defaults. | Expand provider default model lists where real-world traffic demands, driven by channel telemetry rather than upstream catalog crawls. |

## Priority 2: Provider and Relay Depth

| Area | Current State | Needed Work |
| --- | --- | --- |
| Provider-native adapters | Anthropic, Gemini, Azure, and the OpenAI-compatible family have adapters; eight providers explicitly return `requires a native provider adapter`: Hunyuan, Xingchen, Bedrock, Cloudflare, VertexAI, Replicate, Baidu, Xunfei. | **Paused 2026-05-20.** Add native adapters in demand order — each must cover request conversion, response conversion, streaming, usage extraction, and error mapping. See "Provider-Native Adapters — Paused" below for the reason and the order to resume in. |

## Provider-Native Adapters — Paused

This phase is **on hold as of 2026-05-20** and should not be picked up without the prerequisites below.

Reason: each of the eight providers has a non-trivial, provider-specific auth model — Bedrock SigV4, Xunfei HMAC + WebSocket, Baidu access-token OAuth flow with caching, VertexAI GCP service-account JWT, Hunyuan Tencent Cloud signature v3, plus simpler Bearer-token shapes for Cloudflare/Replicate. A pure code-port from upstream one-api without access to real provider credentials produces adapters that compile and pass mocks but cannot be validated end-to-end; the same is true for the streaming and usage-extraction paths whose quirks only surface against live endpoints.

Resume criteria (any one is enough to unblock the adapter in question):
- Provider sandbox credentials available to the implementer.
- A staging deployment with a real channel of that provider type, so live smoke tests can confirm the adapter before merge.
- Explicit decision to ship a code-only port with a documented "untested against live endpoint" caveat in the adapter file.

Suggested resume order (simplest auth first → highest validation cost last):
1. Cloudflare Workers AI — Bearer token, near OpenAI-compatible JSON shape.
2. Replicate — Bearer token, async prediction-poll model.
3. Baidu (Wenxin) — `access_token` exchange via API key + secret, cached.
4. Hunyuan (Tencent) — Tencent Cloud signature v3.
5. VertexAI — GCP service-account JWT signing.
6. Bedrock (AWS) — SigV4.
7. Xunfei (iFlytek) — HMAC + WebSocket streaming.
8. Xingchen (China Telecom) — least documented; defer until demand is demonstrated.

Each provider should land as its own commit with: adapter implementation, factory.go wiring, request/response/stream/usage/error tests, and provider catalog defaults (base URL, supported models, required config fields). Keep the `requires a native provider adapter` error in `factory.go` for any provider that has not yet been ported, so the channel remains rejected rather than silently routed through the OpenAI-compatible fallback.

## Disabled Placeholder Routes

These routes are intentionally registered as stable disabled placeholders, distinct from NotImplemented OpenAI compatibility shims. They MUST return a stable shape and SHOULD NOT be confused with "not yet implemented" — for the routes below, upstream one-api also does not implement them server-side, so holding a stable rejection here is the parity stance.

| Route | Purpose | Status |
| --- | --- | --- |
| `/api/user/aff_transfer` | Affiliate reward transfer | Upstream one-api does not implement this; intentionally disabled. Invitation bonuses are credited at registration time (gated by `INVITER_BONUS_QUOTA` / `INVITEE_BONUS_QUOTA`), not via a user-triggered transfer. |
| `/api/user/pay`, `/api/user/amount` | Online payment initiation/callback | Upstream one-api does not implement online payment in its backend (only its Air theme frontend calls these routes). Self-hosted deployments use redemption codes via `/api/user/topup`. |
| `/api/oauth/telegram/*` | Telegram OAuth login/bind | Telegram bot config and CSRF model. |

## Completion Plan

Each remaining item should land as a small branch with route-level tests first, then implementation.

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

## Recommended Execution Order

1. Build or migrate the full web frontend against the current `/api/*` compatibility layer.
2. Resume provider-native adapters per the order in "Provider-Native Adapters — Paused" once credentials or staging access are available.

## Documentation Policy

Completed one-off design and implementation plan documents have been moved to `docs/archive/`. This file is the current priority source for remaining One API gaps. Architecture and deployment documents remain as reference material in `docs/`.
