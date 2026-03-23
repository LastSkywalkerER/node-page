import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

/** Sensors are not on the SSE payload; load once per page (no steady polling). */
export function useSensors(hostId?: number | null) {
  const queryKey = hostId != null ? ['sensors', hostId] : ['sensors'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId != null ? `/sensors?host_id=${hostId}` : '/sensors';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
}
