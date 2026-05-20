# Web Frontend Sprint 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove frontend build noise and move the admin console from Sprint 4's client-side table improvements toward backend-backed operations, stronger admin access, and broader smoke coverage.

**Architecture:** Sprint 5 should keep Sprint 4's tested frontend primitives, but harden the platform underneath them. Start by making the CSS/build pipeline deterministic, then add explicit backend contracts for server-side admin list operations and export, then introduce a role-aware admin access path while keeping Admin Token compatibility. Finish by extending E2E coverage over the higher-risk workflows.

**Tech Stack:** React 19, TypeScript 6, Vite 8, Tailwind CSS 4, shadcn/ui, TanStack Query, React Router, Vitest, React Testing Library, MSW, Playwright, Go admin HTTP server.

---

## Scope And Sequencing

Sprint 5 should prioritize removing operational ambiguity before adding broader UI features:

1. **Sprint 5A: CSS Toolchain Cleanup**
   - Stop `npm run build` from emitting lightningcss warnings for Tailwind/shadcn at-rules.
   - Keep generated CSS equivalent for existing light/dark themes and shadcn components.
   - Add a small build-output guard so warnings do not silently return later.

2. **Sprint 5B: Backend-Backed Admin Table Contracts**
   - Define explicit request params for sorting, filtering, pagination, and currently-loaded/full CSV export.
   - Add typed frontend request builders that map table state to backend params.
   - Upgrade Users and Channels first, then Logs and Redemptions once contracts are validated.

3. **Sprint 5C: Role-Aware Admin Access**
   - Keep `ADMIN_TOKEN` support for compatibility.
   - Add frontend support for a backend-provided admin capability snapshot when available.
   - Avoid changing database roles until the backend contract is explicit and tested.

4. **Sprint 5D: Broader E2E Smoke Coverage**
   - Cover admin table sort/filter/export paths with mocked backend responses.
   - Cover mobile navigation and preference persistence after reload.
   - Keep E2E tests mocked unless a stable local fixture server becomes available.

## Non-Goals

- Do not rewrite the design system or replace shadcn/ui.
- Do not internationalize the UI in Sprint 5.
- Do not remove Admin Token until backend role/capability contracts are shipped and exercised.
- Do not implement full-history export through frontend-only pagination loops; add a backend export contract first.
- Do not introduce a global frontend state library.

---

### Task 1: Clean Up CSS Build Warnings

**Files:**
- Modify: `web/package.json`
- Modify: `web/vite.config.ts`
- Modify: `web/src/index.css`
- Create: `web/scripts/assert-build-clean.mjs`
- Test: `web/src/components/ThemeToggle.test.tsx` or `web/src/test/css-build.test.ts` only if a stable unit-level assertion is practical.

- [ ] **Step 1: Reproduce current build warnings**

Run:

```bash
cd web
npm run build
```

Expected before the fix: build exits 0 but prints `Unknown at rule` warnings for `@theme`, `@tailwind`, `@utility`, `@custom-variant`, or `@apply`.

- [ ] **Step 2: Identify whether warnings come from Vite minification or Tailwind processing**

Temporarily inspect the generated CSS path and Vite CSS pipeline:

```bash
cd web
npm run build -- --debug
```

Expected: confirm whether lightningcss is seeing unprocessed Tailwind 4 CSS directives from `tailwindcss`, `tw-animate-css`, `shadcn/tailwind.css`, or local `src/index.css`.

- [ ] **Step 3: Add a build-output guard script**

Create `web/scripts/assert-build-clean.mjs`:

```js
import { spawnSync } from 'node:child_process';

const result = spawnSync('npm', ['run', 'build'], {
  cwd: new URL('..', import.meta.url),
  encoding: 'utf8',
  shell: false,
});

const output = `${result.stdout || ''}\n${result.stderr || ''}`;
process.stdout.write(result.stdout || '');
process.stderr.write(result.stderr || '');

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

if (/Unknown at rule/i.test(output)) {
  console.error('Build emitted unexpected CSS at-rule warnings.');
  process.exit(1);
}
```

- [ ] **Step 4: Wire the guard script**

Update `web/package.json`:

```json
{
  "scripts": {
    "build:clean": "node scripts/assert-build-clean.mjs"
  }
}
```

- [ ] **Step 5: Run guard to verify it fails before the CSS pipeline fix**

Run:

```bash
cd web
npm run build:clean
```

Expected: FAIL with `Build emitted unexpected CSS at-rule warnings.`

- [ ] **Step 6: Fix CSS processing at the source**

Use the smallest confirmed fix from Step 2. Prefer one of these, in order:

1. Configure Vite/Tailwind so Tailwind 4 directives are processed before lightningcss minification.
2. Disable lightningcss minification for CSS if Tailwind 4 plugin output is valid but lightningcss does not support the directives.
3. Move third-party Tailwind directive imports to the supported Tailwind 4 import order if the current order is causing unprocessed directives.

Do not delete shadcn/tailwind styles to silence warnings.

- [ ] **Step 7: Verify clean build**

Run:

```bash
cd web
npm run build:clean
npm run test
npm run lint
```

Expected: all exit 0 and `build:clean` prints no `Unknown at rule` warnings.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/vite.config.ts web/src/index.css web/scripts/assert-build-clean.mjs
git commit -m "fix(web): clean css build pipeline"
```

---

### Task 2: Add Admin Table Query Contract Helpers

**Files:**
- Create: `web/src/lib/admin-table-query.ts`
- Test: `web/src/lib/admin-table-query.test.ts`
- Modify: `web/src/hooks/useAdminTableState.ts`
- Modify: `web/src/hooks/useAdminTableState.test.tsx`

- [ ] **Step 1: Write failing query helper tests**

Create `web/src/lib/admin-table-query.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { buildAdminListParams } from './admin-table-query';

describe('buildAdminListParams', () => {
  it('serializes pagination search sorting and filters', () => {
    const params = buildAdminListParams({
      page: 2,
      pageSize: 50,
      search: 'alice',
      sortKey: 'username',
      sortDirection: 'asc',
      filters: { status: '1', group: 'default', empty: '' },
    });

    expect(params.toString()).toBe('page=2&page_size=50&keyword=alice&sort=username&order=asc&status=1&group=default');
  });
});
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
cd web
npm run test -- admin-table-query
```

Expected: FAIL because `admin-table-query.ts` does not exist.

- [ ] **Step 3: Implement query helper**

Create `web/src/lib/admin-table-query.ts`:

```ts
import type { SortDirection } from './table-utils';

interface BuildAdminListParamsOptions {
  page: number;
  pageSize: number;
  search?: string;
  sortKey?: string | null;
  sortDirection?: SortDirection;
  filters?: Record<string, string | number | null | undefined>;
}

export function buildAdminListParams({
  page,
  pageSize,
  search,
  sortKey,
  sortDirection,
  filters = {},
}: BuildAdminListParamsOptions) {
  const params = new URLSearchParams();
  params.set('page', String(page));
  params.set('page_size', String(pageSize));
  if (search?.trim()) params.set('keyword', search.trim());
  if (sortKey && sortDirection) {
    params.set('sort', sortKey);
    params.set('order', sortDirection);
  }
  for (const [key, value] of Object.entries(filters)) {
    if (value !== null && value !== undefined && value !== '') {
      params.set(key, String(value));
    }
  }
  return params;
}
```

- [ ] **Step 4: Extend table state with sort and filters**

Modify `web/src/hooks/useAdminTableState.ts` to expose:
- `sortKey`
- `sortDirection`
- `setSort`
- `filters`
- `setFilter`
- URL persistence for `sort`, `order`, and named filters

Keep existing `page`, `pageSize`, and `search` behavior compatible.

- [ ] **Step 5: Add hook tests**

Extend `web/src/hooks/useAdminTableState.test.tsx` to cover:
- initializes sort and filters from URL
- resets page to `1` when a filter changes
- removes empty filter values from URL

- [ ] **Step 6: Run tests**

Run:

```bash
cd web
npm run test -- admin-table-query useAdminTableState
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/admin-table-query.ts web/src/lib/admin-table-query.test.ts web/src/hooks/useAdminTableState.ts web/src/hooks/useAdminTableState.test.tsx
git commit -m "feat(web): add admin table query contract helpers"
```

---

### Task 3: Upgrade Admin Tables To Server-Backed Params

**Files:**
- Modify: `web/src/pages/admin/UsersPage.tsx`
- Modify: `web/src/pages/admin/ChannelsPage.tsx`
- Modify: `web/src/pages/admin/LogsPage.tsx`
- Modify: `web/src/pages/admin/RedemptionsPage.tsx`
- Modify: `web/e2e/fixtures.ts`
- Modify: `web/e2e/admin-smoke.spec.ts`

- [ ] **Step 1: Add failing E2E coverage for one table contract**

Extend `web/e2e/admin-smoke.spec.ts` with a Users table test:

```ts
test('admin users sends sort and filter params', async ({ page }) => {
  const requests: string[] = [];
  await page.route('**/api/user**', async (route) => {
    if (route.request().method() === 'GET') {
      requests.push(route.request().url());
      await route.fulfill({ json: { success: true, data: [] } });
      return;
    }
    await route.continue();
  });

  await page.goto('/login');
  await page.evaluate(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.setItem('adminToken', 'test-admin-token');
  });
  await page.goto('/admin/users');
  await page.getByLabel('Filter users by status').selectOption('1');
  await page.getByRole('button', { name: /sort by username/i }).click();

  expect(requests.some((url) => url.includes('status=1') && url.includes('sort=username') && url.includes('order=asc'))).toBe(true);
});
```

- [ ] **Step 2: Run E2E to verify failure**

Run:

```bash
cd web
npm run test:e2e
```

Expected: FAIL because pages still sort/filter mostly on the client side.

- [ ] **Step 3: Refactor Users page query**

In `web/src/pages/admin/UsersPage.tsx`:
- Use `useAdminTableState({ storageKey: 'users', filters: ['status', 'group'] })`.
- Build params with `buildAdminListParams`.
- Remove current-page status/group filtering once server params are sent.
- Keep client-side sorting only as fallback if backend ignores sort params, but do not mutate URL separately.

- [ ] **Step 4: Refactor Channels page query**

In `web/src/pages/admin/ChannelsPage.tsx`:
- Send `status`, `type`, `sort`, and `order` through `buildAdminListParams`.
- Preserve current visible UI controls.

- [ ] **Step 5: Refactor Logs page query**

In `web/src/pages/admin/LogsPage.tsx`:
- Use `page`, `pageSize`, `user_id`, `type`, `sort`, and `order`.
- Replace hardcoded `50` with shared page size state.

- [ ] **Step 6: Refactor Redemptions page query**

In `web/src/pages/admin/RedemptionsPage.tsx`:
- Use `page`, `pageSize`, `keyword`, `status`, `sort`, and `order`.
- Replace hardcoded `20` with shared page size state.

- [ ] **Step 7: Run tests**

Run:

```bash
cd web
npm run test
npm run test:e2e
npm run lint
npm run build:clean
```

Expected: all exit 0.

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/admin web/e2e web/src/lib/admin-table-query.ts
git commit -m "feat(web): send admin table sort and filter params"
```

---

### Task 4: Define Backend Export Contract

**Files:**
- Modify: `internal/admin/server/http.go`
- Modify: `internal/admin/server/http_test.go`
- Modify: `web/src/components/admin/ExportButton.tsx`
- Modify: `web/src/pages/admin/UsersPage.tsx`
- Modify: `web/src/pages/admin/ChannelsPage.tsx`
- Modify: `web/src/pages/admin/LogsPage.tsx`
- Modify: `web/src/pages/admin/RedemptionsPage.tsx`
- Test: `web/src/components/admin/ExportButton.test.tsx`

- [ ] **Step 1: Add backend route tests**

Add tests in `internal/admin/server/http_test.go` covering:
- `GET /api/user/export?format=csv`
- `GET /api/channel/export?format=csv`
- response has `Content-Type: text/csv`
- route requires admin auth

- [ ] **Step 2: Run backend tests to verify failure**

Run:

```bash
go test ./internal/admin/server
```

Expected: FAIL because export routes do not exist.

- [ ] **Step 3: Add backend route stubs**

In `internal/admin/server/http.go`, add authenticated export endpoints that initially return currently supported rows from existing service list methods. Use explicit `format=csv`; reject unsupported formats with HTTP 400.

- [ ] **Step 4: Add frontend export mode**

Modify `web/src/components/admin/ExportButton.tsx` to support two modes:
- `rows + columns`: current Sprint 4 client-side export
- `href`: backend export link generated from current table params

Keep the button disabled if neither rows nor href is available.

- [ ] **Step 5: Wire pages to backend export links**

For each admin table page:
- Build an export URL from current `buildAdminListParams`.
- Append `format=csv`.
- Pass it to `ExportButton`.

- [ ] **Step 6: Run verification**

Run:

```bash
go test ./internal/admin/server
cd web
npm run test
npm run lint
npm run build:clean
```

Expected: all exit 0.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/server web/src/components/admin web/src/pages/admin
git commit -m "feat(admin): add csv export contract"
```

---

### Task 5: Add Role-Aware Admin Capability Snapshot

**Files:**
- Modify: `internal/admin/server/http.go`
- Modify: `internal/admin/server/http_test.go`
- Create: `web/src/lib/admin-access.ts`
- Test: `web/src/lib/admin-access.test.ts`
- Modify: `web/src/components/AppNavigation.tsx`
- Modify: `web/e2e/admin-smoke.spec.ts`
- Modify: `web/e2e/fixtures.ts`

- [ ] **Step 1: Write frontend access helper tests**

Create `web/src/lib/admin-access.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { canAccessAdmin } from './admin-access';

describe('canAccessAdmin', () => {
  it('allows admin token compatibility', () => {
    expect(canAccessAdmin({ adminToken: 'token' })).toBe(true);
  });

  it('allows backend capability snapshot', () => {
    expect(canAccessAdmin({ snapshot: { admin: true } })).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
cd web
npm run test -- admin-access
```

Expected: FAIL because helper does not exist.

- [ ] **Step 3: Implement helper**

Create `web/src/lib/admin-access.ts`:

```ts
export interface AdminAccessSnapshot {
  admin?: boolean;
}

export function canAccessAdmin({ adminToken, snapshot }: { adminToken?: string | null; snapshot?: AdminAccessSnapshot | null }) {
  return Boolean(adminToken || snapshot?.admin);
}
```

- [ ] **Step 4: Add backend snapshot endpoint**

In `internal/admin/server/http.go`, add `GET /api/admin/access`:
- Returns `{ "success": true, "data": { "admin": true } }` when Admin Token auth succeeds.
- Returns 401 for invalid Admin Token.
- Keep it compatible with current manual token flow.

- [ ] **Step 5: Add backend tests**

In `internal/admin/server/http_test.go`, cover:
- valid admin token returns `admin: true`
- missing token returns 401

- [ ] **Step 6: Wire navigation**

Modify `web/src/components/AppNavigation.tsx`:
- Keep local `adminToken` entry dialog.
- Query `/api/admin/access` when `adminToken` exists.
- Show admin nav if `canAccessAdmin` returns true.
- If snapshot request returns 401, clear admin token and hide admin nav.

- [ ] **Step 7: Update E2E fixtures**

In `web/e2e/fixtures.ts`, mock `GET /api/admin/access`.

- [ ] **Step 8: Run verification**

Run:

```bash
go test ./internal/admin/server
cd web
npm run test
npm run test:e2e
npm run lint
npm run build:clean
```

Expected: all exit 0.

- [ ] **Step 9: Commit**

```bash
git add internal/admin/server web/src/lib/admin-access.ts web/src/lib/admin-access.test.ts web/src/components/AppNavigation.tsx web/e2e
git commit -m "feat(web): add admin access capability snapshot"
```

---

### Task 6: Expand E2E Smoke Coverage

**Files:**
- Modify: `web/e2e/admin-smoke.spec.ts`
- Modify: `web/e2e/fixtures.ts`
- Modify: `web/playwright.config.ts` only if viewport projects are added.

- [ ] **Step 1: Add mobile viewport project**

If runtime remains stable with local Chrome, add a mobile project in `web/playwright.config.ts`:

```ts
{
  name: 'mobile-chrome',
  use: {
    ...devices['Pixel 5'],
    channel: 'chrome',
  },
}
```

- [ ] **Step 2: Add mobile navigation test**

Add E2E coverage:
- authenticated user opens mobile nav
- admin token exposes Options link
- menu closes after navigation

- [ ] **Step 3: Add preference persistence test**

Add E2E coverage:
- set page size on Users table
- reload page
- page size remains selected

- [ ] **Step 4: Add export route test**

Add E2E coverage:
- click backend export button
- verify request URL contains current filters and `format=csv`

- [ ] **Step 5: Run E2E**

Run:

```bash
cd web
npm run test:e2e
```

Expected: PASS in desktop and mobile projects.

- [ ] **Step 6: Run full verification**

Run:

```bash
cd web
npm run test
npm run test:e2e
npm run lint
npm run build:clean
go test ./internal/admin/server
```

Expected: all exit 0.

- [ ] **Step 7: Commit**

```bash
git add web/e2e web/playwright.config.ts
git commit -m "test(web): expand admin e2e smoke coverage"
```

---

## Acceptance Criteria

- `npm run build:clean` exits 0 and emits no `Unknown at rule` warnings.
- `npm run test`, `npm run test:e2e`, `npm run lint`, and `go test ./internal/admin/server` pass.
- Admin table pages send explicit backend params for pagination, search, filters, sort key, and sort direction.
- CSV export uses a documented backend contract for full export, while current-page client export remains available only where no backend route exists.
- Admin navigation can be driven by Admin Token compatibility or a backend capability snapshot.
- Mobile navigation and preference persistence have E2E coverage.

## Risks And Follow-Ups

- Tailwind 4 and shadcn CSS processing may require a package/plugin adjustment. Keep the first task tightly scoped and verify generated styles visually before moving on.
- Backend sort/filter/export support may expose gaps in current service methods. If service-layer contracts are missing, split backend implementation into smaller tasks rather than encoding SQL behavior in HTTP handlers.
- Role-based admin access still depends on backend identity semantics. Sprint 5 should add capability snapshot compatibility first, not a full role migration.
- Full export can be expensive for large datasets. Add row limits, streaming, or async export later if production volume requires it.
