import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';
import type { MetricFetchMode } from '../../shared/types/metricFetch';

export function useMemory(hostId?: number | null, options?: { mode?: MetricFetchMode }) {
  const mode = options?.mode ?? 'snapshot';
  const poll = mode === 'poll';
  const queryKey = hostId != null ? ['memory-metrics', hostId] : ['memory-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId != null ? `/memory?host_id=${hostId}` : '/memory';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: poll ? 5000 : false,
    staleTime: poll ? 1000 : Infinity,
    refetchOnWindowFocus: poll,
  });
}
