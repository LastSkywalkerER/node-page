import { useQuery } from '@tanstack/react-query';
import type { MetricFetchMode } from '../../shared/types/metricFetch';
import { apiClient } from '../../shared/lib/api';

/**
 * snapshot: one REST load (history + DB latest), live updates via SSE → useLiveMetricsQuerySync.
 * poll: legacy interval refetch (e.g. no stream).
 */
export function useCPU(hostId?: number | null, options?: { mode?: MetricFetchMode }) {
  const mode = options?.mode ?? 'snapshot';
  const poll = mode === 'poll';
  const queryKey = hostId != null ? ['cpu-metrics', hostId] : ['cpu-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId != null ? `/cpu?host_id=${hostId}` : '/cpu';
      const { data } = await apiClient.get(url);
      return data;
    },
    refetchInterval: poll ? 5000 : false,
    staleTime: poll ? 1000 : Infinity,
    refetchOnWindowFocus: poll,
  });
}
