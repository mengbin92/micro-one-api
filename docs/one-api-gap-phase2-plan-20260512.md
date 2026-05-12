# One API Gap Phase 2 Plan

> Branch: `feature/one-api-gap-phase2`
> Date: 2026-05-12

## Goal

Align the existing One API compatibility layer with the fields and response shapes expected by the upstream One API frontend and common API clients.

## Scope

Phase 2 starts with API/data compatibility, not web UI or new provider adapters.

1. Token field parity:
   - Add `created_time`, `accessed_time`, `used_quota`, and `subnet`.
   - Preserve existing `created_at` for current callers while returning One API field names.
   - Accept `subnet`, quota, and unlimited quota in token create/update bodies.
2. Channel field parity:
   - Add response and persistence fields for weight, test time, response time, balance, balance updated time, used quota, model mapping, and system prompt.
3. Option key parity:
   - Expand `/api/option/` beyond the current minimal `site_title` and `registration_enabled` mapping.
4. Admin/user route cleanup:
   - Fill small One API route gaps that unblock frontend compatibility.

## Non-goals

1. Full web frontend.
2. Provider-specific balance refresh implementation.
3. Large schema rewrites or destructive migrations.
4. New provider native adapters.

## Execution Order

1. Token fields and tests.
2. Channel fields and tests.
3. Option key expansion.
4. Small route gaps discovered by frontend/API compatibility checks.

## Verification

Each step must use TDD and finish with:

```bash
go test ./...
```
