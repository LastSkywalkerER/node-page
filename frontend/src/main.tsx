import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import App from './App.tsx';
import './index.css';

/**
 * Create a React Query client with optimized settings for system monitoring.
 * This client handles API requests with exponential backoff retry logic,
 * appropriate stale times for real-time data, and garbage collection settings.
 */
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 3, // Retry failed requests up to 3 times
      retryDelay: (attemptIndex) => Math.min(1000 * 2 ** attemptIndex, 30000), // Exponential backoff with max 30s
      staleTime: 5000, // Consider data stale after 5 seconds for real-time updates
      gcTime: 10 * 60 * 1000, // Keep unused data in cache for 10 minutes
    },
  },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </React.StrictMode>
);
