import { useQuery } from '@tanstack/react-query';

// Hook for CPU metrics - updates every 5 seconds
export function useCPU(hostId?: number | null) {
  const queryKey = hostId ? ['cpu-metrics', hostId] : ['cpu-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/api/cpu?host_id=${hostId}` : '/api/cpu';
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch CPU metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
