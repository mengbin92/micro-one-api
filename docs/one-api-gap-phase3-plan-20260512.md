# One API Gap Phase 3 Plan

> Branch: `feature/one-api-gap-phase3`
> Date: 2026-05-12

## Goal

Continue the One API compatibility work after phase 1 usage/dashboard aggregation and phase 2 token/channel/option field parity by filling common admin route compatibility gaps.

## Scope

Phase 3 focuses on thin HTTP route aliases that can reuse existing service capabilities without adding new storage semantics:

1. User admin route parity:
   - `/api/user/`
   - `/api/user/search`
   - `/api/user/{id}`
   - Create, update, delete, list, search, and get use the existing identity service admin calls.
2. Channel admin route parity:
   - `/api/channel/`
   - `/api/channel/search`
   - `/api/channel/models`
   - `/api/channel/{id}`
   - Create, update, delete, list, search, and get use the existing channel service admin calls.
3. Log admin route parity:
   - `/api/log/`
   - `/api/log/search`
   - List and search use existing billing-backed log listing.

## Non-goals

1. Full web frontend.
2. Provider-specific balance adapters.
3. Destructive schema changes.
4. Historical log deletion until the underlying log/billing service exposes safe delete semantics.
5. OAuth/OIDC/Lark/WeChat full bind and callback flows.

## Verification

Use TDD for each route group and finish with:

```bash
go test ./...
```
