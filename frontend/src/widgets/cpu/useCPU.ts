import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

// Hook for CPU metrics - updates every 5 seconds
export function useCPU(hostId?: number | null) {
  const queryKey = hostId ? ['cpu-metrics', hostId] : ['cpu-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/cpu?host_id=${hostId}` : '/cpu';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
