import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

export function useSensors(hostId?: number | null) {
  const queryKey = hostId ? ['sensors', hostId] : ['sensors'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/sensors?host_id=${hostId}` : '/sensors';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}


