import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

// Hook for Memory metrics - updates every 5 seconds
export function useMemory(hostId?: number | null) {
  const queryKey = hostId ? ['memory-metrics', hostId] : ['memory-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/memory?host_id=${hostId}` : '/memory';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
