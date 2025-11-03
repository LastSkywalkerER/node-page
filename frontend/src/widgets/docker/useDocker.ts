import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

// Hook for Docker metrics - updates every 5 seconds
export function useDocker(hostId?: number | null) {
  const queryKey = hostId ? ['docker-metrics', hostId] : ['docker-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/docker?host_id=${hostId}` : '/docker';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
