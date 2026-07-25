import { RouterProvider } from 'react-router';
import { QueryClientProvider } from '@tanstack/react-query';
import { Toaster } from 'sonner';
import { router } from './router';
import { queryClient } from '@/lib/query-client';

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster richColors closeButton position="top-right" />
    </QueryClientProvider>
  );
}

export default App;
