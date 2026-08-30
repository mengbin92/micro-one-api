import { RouterProvider } from 'react-router';
import { QueryClientProvider } from '@tanstack/react-query';
import { Toaster } from 'sonner';
import { router } from './router';
import { queryClient } from '@/lib/query-client';
import { I18nProvider } from '@/components/I18nProvider';
import { useI18n } from '@/hooks/useI18n';

function LocalizedApp() {
  const { language } = useI18n();

  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider key={language} router={router} />
      <Toaster richColors closeButton position="top-right" />
    </QueryClientProvider>
  );
}

function App() {
  return (
    <I18nProvider>
      <LocalizedApp />
    </I18nProvider>
  );
}

export default App;
