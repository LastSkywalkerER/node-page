import { useQuery } from '@tanstack/react-query';

// Hook for CPU metrics - updates every 5 seconds
export function useCPU() {
  return useQuery({
    queryKey: ['cpu-metrics'],
    queryFn: async () => {
      const response = await fetch('/api/cpu');
      if (!response.ok) {
        throw new Error('Failed to fetch CPU metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
