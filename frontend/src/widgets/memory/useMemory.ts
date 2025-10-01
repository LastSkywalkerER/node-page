import { useQuery } from '@tanstack/react-query';

// Hook for Memory metrics - updates every 5 seconds
export function useMemory() {
  return useQuery({
    queryKey: ['memory-metrics'],
    queryFn: async () => {
      const response = await fetch('/api/memory');
      if (!response.ok) {
        throw new Error('Failed to fetch memory metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
