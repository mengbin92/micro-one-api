import { defineConfig, mergeConfig } from 'vitest/config';
import base from './vite.config';

// Measure money, authorization/CAS, and cancellation paths separately from
// unrelated UI code. Component regressions remain part of the regular suite.
export default mergeConfig(base, defineConfig({
  test: {
    include: [
      'src/lib/{amount,admin-write,authorization,relay-playground,sse}.test.{ts,tsx}',
      'src/pages/{PlaygroundPage,OrdersPage,SubscriptionsPage}.test.tsx',
      'src/pages/admin/UsersPage.test.tsx',
      'src/components/admin/IAMChangeDialog.test.tsx',
      // Management action gates exercise both execution and display snapshots
      // during revalidation, including any/all and object-level permissions.
      'src/components/admin/PermissionButton.test.tsx',
      'src/pages/admin/ChannelsPage.refresh.test.tsx',
      'src/pages/admin/ChannelsPage.preconditions.test.tsx',
      'src/components/AdminRoute.test.tsx',
      'src/components/ProtectedRoute.test.tsx',
      'src/components/playground/AssistantMarkdown.test.tsx',
    ],
    coverage: {
      provider: 'v8',
      include: [
        'src/lib/{amount,admin-write,authorization,relay-playground,sse}.ts',
        'src/pages/PlaygroundPage.tsx',
        'src/components/AdminRoute.tsx',
        'src/components/playground/{AssistantContent,AssistantMarkdown}.tsx',
      ],
      reporter: ['text', 'json-summary', 'html'],
      reportsDirectory: 'coverage/critical',
      thresholds: {
        branches: 84, lines: 92, functions: 91, statements: 90,
        'src/lib/amount.ts': { branches: 100, lines: 100, functions: 100, statements: 100 },
        'src/lib/admin-write.ts': { branches: 93, lines: 97 },
        'src/lib/authorization.ts': { branches: 85, lines: 96 },
        'src/lib/relay-playground.ts': { branches: 89, lines: 96 },
        'src/lib/sse.ts': { branches: 100, lines: 100 },
        'src/components/playground/AssistantContent.tsx': { branches: 100, lines: 100 },
        'src/components/playground/AssistantMarkdown.tsx': { branches: 85, lines: 100 },
        'src/pages/PlaygroundPage.tsx': { branches: 75, lines: 84 },
      },
    },
  },
}));
