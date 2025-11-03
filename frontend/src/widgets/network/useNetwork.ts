import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

// Hook for Network metrics - updates every 5 seconds
export function useNetwork(hostId?: number | null) {
  const queryKey = hostId ? ['network-metrics', hostId] : ['network-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/network?host_id=${hostId}` : '/network';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
