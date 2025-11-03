import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

// Hook for Disk metrics - updates every 5 seconds
export function useDisk(hostId?: number | null) {
  const queryKey = hostId ? ['disk-metrics', hostId] : ['disk-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/disk?host_id=${hostId}` : '/disk';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
