# Web Frontend Sprint 4+ Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the current Sprint 3 frontend into a safer, more scalable admin console with tests, typed API boundaries, richer tables, and usable mobile behavior.

**Architecture:** Sprint 4 should be split into independently shippable tracks. Start with test infrastructure and typed API helpers so later UI changes can be verified; then add table capabilities through shared primitives; then improve responsive navigation and user preferences. i18n should remain a later opt-in unless there is a concrete need for multiple languages.

**Tech Stack:** React 19, TypeScript 6, Vite 8, TanStack Query, React Router, shadcn/ui, Tailwind CSS, Vitest, React Testing Library, MSW, Playwright.

---

## Scope And Sequencing

Sprint 4 should prioritize reliability before feature breadth:

1. **Sprint 4A: Test foundation + typed API helpers**
   - Add Vitest, React Testing Library, jsdom, and MSW.
   - Add typed response unwrapping helpers around `apiClient` / `adminApiClient`.
   - Cover login, API error extraction, query error toast behavior, and one table page.

2. **Sprint 4B: Shared table shell**
   - Extract reusable admin table state for pagination, page size, search, status/type filters, and URL query params.
   - Add client-side column sorting where backend sorting is unavailable.
   - Add CSV export for currently loaded rows first; defer server-side full export until backend contract is explicit.

3. **Sprint 4C: Mobile and preferences**
   - Add responsive navigation with a drawer or collapsible menu.
   - Add table column visibility presets for narrow screens.
   - Persist user preferences: theme, page size, visible columns, and optional timezone.

4. **Sprint 4D: E2E smoke tests**
   - Add Playwright smoke coverage for login redirect, protected layout, admin token flow, and Options page render.
   - Mock backend responses unless a stable local fixture server is available.

5. **Sprint 4+ later: i18n**
   - Add i18n only after product language requirements are concrete.
   - If needed, start with a small translation dictionary for navigation and table chrome; do not translate backend data.

## Non-Goals

- Do not change backend API semantics unless a UI feature cannot be delivered safely without it.
- Do not build full server-side export in Sprint 4B; current `/api/*` list responses do not expose a dedicated export contract.
- Do not add a global state library; TanStack Query plus small local hooks remains enough.
- Do not internationalize all user-facing strings until language scope is explicit.

---

### Task 1: Add Frontend Test Foundation

**Files:**
- Modify: `web/package.json`
- Modify: `web/vite.config.ts`
- Modify: `web/eslint.config.js`
- Create: `web/src/test/setup.ts`
- Create: `web/src/test/msw/server.ts`
- Create: `web/src/test/render.tsx`
- Move/Modify: `web/test/api-error.test.ts` -> `web/src/lib/api-error.test.ts`

- [ ] **Step 1: Install test dependencies**

Run:

```bash
cd web
npm install --save-dev vitest @testing-library/react @testing-library/user-event @testing-library/jest-dom jsdom msw
```

- [ ] **Step 2: Add scripts**

Update `web/package.json`:

```json
{
  "scripts": {
    "test": "vitest run",
    "test:watch": "vitest",
    "test:ui": "vitest --ui"
  }
}
```

Keep `npm run test:api` only if still useful; otherwise fold it into `vitest run`.

- [ ] **Step 3: Configure Vitest**

Update `web/vite.config.ts` with:

```ts
/// <reference types="vitest" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    globals: true,
  },
});
```

- [ ] **Step 4: Add setup file**

Create `web/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest';
import { afterAll, afterEach, beforeAll, vi } from 'vitest';
import { server } from './msw/server';

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => {
  server.resetHandlers();
  localStorage.clear();
  vi.restoreAllMocks();
});
afterAll(() => server.close());
```

- [ ] **Step 5: Add MSW server**

Create `web/src/test/msw/server.ts`:

```ts
import { setupServer } from 'msw/node';

export const server = setupServer();
```

- [ ] **Step 6: Add render helper**

Create `web/src/test/render.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactElement } from 'react';

export function renderWithQuery(ui: ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}
```

- [ ] **Step 7: Run test command to verify baseline**

Run:

```bash
cd web
npm run test
```

Expected: PASS with the migrated `api-error` test.

- [ ] **Step 8: Verify build and lint**

Run:

```bash
cd web
npm run lint
npm run build
```

Expected: both exit 0. Existing Tailwind/shadcn lightningcss warnings during build are acceptable until a CSS toolchain task addresses them.

- [ ] **Step 9: Commit**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/eslint.config.js web/src/test web/src/lib/api-error.test.ts
git commit -m "test(web): add frontend test foundation"
```

---

### Task 2: Add Typed API Helpers

**Files:**
- Create: `web/src/lib/api-response.ts`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/LoginPage.tsx`
- Modify: `web/src/pages/TokensPage.tsx`
- Modify: `web/src/pages/admin/OptionsPage.tsx`
- Test: `web/src/lib/api-response.test.ts`

- [ ] **Step 1: Write failing tests for response unwrapping**

Create `web/src/lib/api-response.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { unwrapApiData, ensureApiSuccess } from './api-response';

describe('api response helpers', () => {
  it('returns data from one-api response envelopes', () => {
    expect(unwrapApiData({ success: true, data: [{ id: 1 }] })).toEqual([{ id: 1 }]);
  });

  it('throws backend message when success is false', () => {
    expect(() => ensureApiSuccess({ success: false, message: 'bad option' })).toThrow('bad option');
  });

  it('throws fallback when response has no message', () => {
    expect(() => ensureApiSuccess({ success: false })).toThrow('Request failed');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web
npm run test -- api-response
```

Expected: FAIL because `api-response.ts` does not exist.

- [ ] **Step 3: Implement helpers**

Create `web/src/lib/api-response.ts`:

```ts
export interface ApiEnvelope<T = unknown> {
  success?: boolean;
  message?: string;
  data?: T;
}

export function ensureApiSuccess(response: ApiEnvelope, fallback = 'Request failed') {
  if (response.success === false) {
    throw new Error(response.message || fallback);
  }
}

export function unwrapApiData<T>(response: ApiEnvelope<T>, fallback = 'Request failed'): T {
  ensureApiSuccess(response, fallback);
  return response.data as T;
}
```

- [ ] **Step 4: Refactor representative pages**

Use `unwrapApiData<T>(res.data)` in pages that currently do `res.data.data as T`.

Start with:
- `web/src/pages/LoginPage.tsx`
- `web/src/pages/TokensPage.tsx`
- `web/src/pages/admin/OptionsPage.tsx`

Do not refactor all pages in one commit unless tests cover the shared helper.

- [ ] **Step 5: Run tests and build**

```bash
cd web
npm run test
npm run lint
npm run build
```

Expected: all exit 0.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api-response.ts web/src/lib/api-response.test.ts web/src/pages/LoginPage.tsx web/src/pages/TokensPage.tsx web/src/pages/admin/OptionsPage.tsx
git commit -m "refactor(web): add typed api response helpers"
```

---

### Task 3: Add Shared Admin Table State

**Files:**
- Create: `web/src/components/admin/AdminTableToolbar.tsx`
- Create: `web/src/components/admin/AdminPagination.tsx`
- Create: `web/src/hooks/useAdminTableState.ts`
- Modify: `web/src/pages/admin/UsersPage.tsx`
- Modify: `web/src/pages/admin/ChannelsPage.tsx`
- Test: `web/src/hooks/useAdminTableState.test.tsx`

- [ ] **Step 1: Write hook tests**

Test behaviors:
- initializes `page`, `pageSize`, and `search` from URL params
- resets page to `1` when search changes
- writes updated params back to URL
- persists page size to `localStorage`

- [ ] **Step 2: Run hook tests to verify they fail**

```bash
cd web
npm run test -- useAdminTableState
```

Expected: FAIL because hook does not exist.

- [ ] **Step 3: Implement hook**

Create `useAdminTableState` with:
- `page`
- `setPage`
- `pageSize`
- `setPageSize`
- `search`
- `setSearch`
- `filters`
- URL synchronization via React Router search params

- [ ] **Step 4: Extract pagination component**

Create `AdminPagination` props:

```ts
interface AdminPaginationProps {
  page: number;
  pageSize: number;
  hasNextPage: boolean;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
}
```

- [ ] **Step 5: Extract toolbar component**

Create `AdminTableToolbar` for search, clear, and optional right-side actions.

- [ ] **Step 6: Refactor Users and Channels**

Replace duplicated search/pagination code in:
- `web/src/pages/admin/UsersPage.tsx`
- `web/src/pages/admin/ChannelsPage.tsx`

Keep API params compatible with existing `page` and `page_size`.

- [ ] **Step 7: Run tests, lint, build**

```bash
cd web
npm run test
npm run lint
npm run build
```

Expected: all exit 0.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/admin web/src/hooks web/src/pages/admin/UsersPage.tsx web/src/pages/admin/ChannelsPage.tsx
git commit -m "feat(web): share admin table state"
```

---

### Task 4: Add Table Sorting, Filters, And CSV Export

**Files:**
- Create: `web/src/lib/csv.ts`
- Create: `web/src/components/admin/SortableHeader.tsx`
- Create: `web/src/components/admin/ExportButton.tsx`
- Modify: `web/src/pages/admin/UsersPage.tsx`
- Modify: `web/src/pages/admin/ChannelsPage.tsx`
- Modify: `web/src/pages/admin/LogsPage.tsx`
- Modify: `web/src/pages/admin/RedemptionsPage.tsx`
- Test: `web/src/lib/csv.test.ts`

- [ ] **Step 1: Write CSV tests**

Create tests covering:
- escaping commas
- escaping quotes
- preserving empty cells
- stable header order

- [ ] **Step 2: Run tests to verify failure**

```bash
cd web
npm run test -- csv
```

Expected: FAIL because `csv.ts` does not exist.

- [ ] **Step 3: Implement CSV helper**

Create `web/src/lib/csv.ts`:

```ts
export function toCsv<T extends Record<string, unknown>>(rows: T[], columns: Array<{ key: keyof T; label: string }>) {
  const escape = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`;
  return [
    columns.map((column) => escape(column.label)).join(','),
    ...rows.map((row) => columns.map((column) => escape(row[column.key])).join(',')),
  ].join('\n');
}
```

- [ ] **Step 4: Add ExportButton**

`ExportButton` should:
- accept filename, rows, columns
- create a `Blob`
- trigger download
- be disabled when there are no rows

- [ ] **Step 5: Add SortableHeader**

`SortableHeader` should:
- render current sort direction
- cycle `none -> asc -> desc -> none`
- use icon buttons where possible

- [ ] **Step 6: Add client-side sorting**

For each page, sort the currently loaded rows only:
- Users: username, email, group, quota, status
- Channels: name, type, group, priority, balance, status
- Logs: userId, type, amount, createdAt
- Redemptions: code, name, amount, status, createdAt

- [ ] **Step 7: Add simple filters**

Add filters that can be supported without backend changes:
- Users: status, group text
- Channels: status, provider type
- Logs: type already exists; add date range only if backend params are wired end-to-end
- Redemptions: status

- [ ] **Step 8: Run tests, lint, build**

```bash
cd web
npm run test
npm run lint
npm run build
```

Expected: all exit 0.

- [ ] **Step 9: Commit**

```bash
git add web/src/lib/csv.ts web/src/lib/csv.test.ts web/src/components/admin web/src/pages/admin
git commit -m "feat(web): add admin table sorting filters and csv export"
```

---

### Task 5: Improve Mobile Navigation And Table Layout

**Files:**
- Create: `web/src/components/AppNavigation.tsx`
- Create: `web/src/components/MobileNav.tsx`
- Create: `web/src/hooks/useMediaQuery.ts`
- Modify: `web/src/components/ProtectedRoute.tsx`
- Modify: admin table pages as needed for responsive column visibility
- Test: `web/src/components/AppNavigation.test.tsx`

- [ ] **Step 1: Write navigation tests**

Cover:
- public nav links render for authenticated users
- admin links render only when admin token exists
- mobile nav opens and closes
- logout clears both tokens

- [ ] **Step 2: Run tests to verify failure**

```bash
cd web
npm run test -- AppNavigation
```

Expected: FAIL because component does not exist.

- [ ] **Step 3: Extract navigation from ProtectedRoute**

Move navigation logic from `ProtectedRoute` to `AppNavigation`.

`ProtectedRoute` should only:
- enforce token
- render layout wrapper
- render `Outlet`

- [ ] **Step 4: Add mobile menu**

Use a compact header plus menu button on small screens.

If current shadcn setup lacks a sheet/drawer component, use an accessible `Dialog` first rather than adding a second overlay library.

- [ ] **Step 5: Add responsive table strategy**

Use column visibility rules:
- Always show primary identifier and status/action columns.
- Hide secondary metadata on small screens.
- Keep `overflow-x-auto` for pages where hiding would remove necessary data.

- [ ] **Step 6: Run tests, lint, build**

```bash
cd web
npm run test
npm run lint
npm run build
```

Expected: all exit 0.

- [ ] **Step 7: Commit**

```bash
git add web/src/components web/src/hooks web/src/pages/admin
git commit -m "feat(web): improve mobile navigation and tables"
```

---

### Task 6: Add User Preferences

**Files:**
- Create: `web/src/lib/preferences.ts`
- Create: `web/src/hooks/usePreference.ts`
- Modify: `web/src/components/ThemeToggle.tsx`
- Modify: `web/src/hooks/useAdminTableState.ts`
- Modify: `web/src/pages/admin/*Page.tsx`
- Test: `web/src/lib/preferences.test.ts`

- [ ] **Step 1: Write preference tests**

Cover:
- returns default when localStorage is empty
- ignores invalid JSON
- persists valid values
- supports namespaced keys

- [ ] **Step 2: Run tests to verify failure**

```bash
cd web
npm run test -- preferences
```

Expected: FAIL because helpers do not exist.

- [ ] **Step 3: Implement preference helpers**

Use localStorage with a `web:` prefix:
- `web:theme`
- `web:admin-page-size`
- `web:admin-visible-columns:<page>`
- `web:timezone`

- [ ] **Step 4: Wire existing theme toggle**

Refactor `ThemeToggle` to use `usePreference('theme')`.

- [ ] **Step 5: Wire page size**

Use preference-backed default page size in `useAdminTableState`.

- [ ] **Step 6: Add timezone display preference only if needed**

Keep default as browser locale. Add timezone selector only if users need consistent server/admin review times across locales.

- [ ] **Step 7: Run tests, lint, build**

```bash
cd web
npm run test
npm run lint
npm run build
```

Expected: all exit 0.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/preferences.ts web/src/lib/preferences.test.ts web/src/hooks web/src/components/ThemeToggle.tsx web/src/pages/admin
git commit -m "feat(web): persist admin user preferences"
```

---

### Task 7: Add Playwright E2E Smoke Tests

**Files:**
- Modify: `web/package.json`
- Create: `web/playwright.config.ts`
- Create: `web/e2e/admin-smoke.spec.ts`
- Create: `web/e2e/fixtures.ts`

- [ ] **Step 1: Install Playwright**

Run:

```bash
cd web
npm install --save-dev @playwright/test
npx playwright install chromium
```

- [ ] **Step 2: Add script**

Update `web/package.json`:

```json
{
  "scripts": {
    "test:e2e": "playwright test"
  }
}
```

- [ ] **Step 3: Add config**

Create `web/playwright.config.ts` with:
- `webServer.command = 'npm run dev -- --host 127.0.0.1'`
- `webServer.url = 'http://127.0.0.1:5173'`
- chromium project only initially

- [ ] **Step 4: Add mocked route fixtures**

Mock:
- `POST /api/user/login`
- `GET /api/user/self`
- `GET /api/user/dashboard`
- `GET /api/option/`

- [ ] **Step 5: Add smoke tests**

Cover:
- unauthenticated `/dashboard` redirects to `/login`
- login stores token and shows dashboard
- admin token enables Options nav
- `/admin/options` renders core settings

- [ ] **Step 6: Run E2E**

```bash
cd web
npm run test:e2e
```

Expected: all smoke tests pass.

- [ ] **Step 7: Run full verification**

```bash
cd web
npm run test
npm run test:e2e
npm run lint
npm run build
```

Expected: all exit 0.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/package-lock.json web/playwright.config.ts web/e2e
git commit -m "test(web): add e2e smoke coverage"
```

---

## Acceptance Criteria

- `npm run test`, `npm run lint`, and `npm run build` pass for every Sprint 4 task.
- `go test ./internal/admin/server` still passes after any route or backend compatibility changes.
- Admin table pages share pagination/search behavior and keep URL state.
- Users can sort, filter, and export the currently loaded table rows.
- Mobile navigation is usable at 375px width without overlapping controls.
- Theme and page size preferences survive reload.
- At least one E2E smoke suite covers auth redirect, login, admin token access, and Options page rendering.

## Risks And Follow-Ups

- Build currently emits lightningcss warnings for Tailwind/shadcn at-rules while exiting 0. Track this separately unless it starts breaking production CSS.
- CSV export of currently loaded rows may not satisfy operators who need full-history exports. Add backend export endpoints only after confirming expected data volume and authorization rules.
- i18n can multiply maintenance cost. Keep it out of Sprint 4 unless there is an explicit language requirement.
- Role-based admin access remains blocked by missing backend/user role contract; do not replace manual Admin Token until that contract exists.
