import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { I18nProvider } from '@/components/I18nProvider';
import { I18nTestBoundary } from '@/test/I18nTestBoundary';

export function renderWithQuery(ui: ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

  return render(
    <I18nProvider>
      <QueryClientProvider client={queryClient}>
        <I18nTestBoundary ui={ui} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}
