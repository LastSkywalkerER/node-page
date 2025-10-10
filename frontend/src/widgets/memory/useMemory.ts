import { useQuery } from '@tanstack/react-query';

// Hook for Memory metrics - updates every 5 seconds
export function useMemory(hostId?: number | null) {
  const queryKey = hostId ? ['memory-metrics', hostId] : ['memory-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/api/memory?host_id=${hostId}` : '/api/memory';
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch memory metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
