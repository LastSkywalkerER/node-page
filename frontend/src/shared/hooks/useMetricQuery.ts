import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../lib/api';
import type { MetricFetchMode } from '../types/metricFetch';

export interface MetricResponse<L, H> {
  latest: L | null;
  history: H[];
}

export function createMetricHook<L, H>(endpoint: string, queryKeyBase: string) {
  return function useMetric(
    hostId?: number | null,
    options?: { mode?: MetricFetchMode }
  ) {
    const mode = options?.mode ?? 'snapshot';
    const poll = mode === 'poll';
    const queryKey = hostId != null ? [queryKeyBase, hostId] : [queryKeyBase];

    return useQuery<MetricResponse<L, H>>({
      queryKey,
      queryFn: async () => {
        const url = hostId != null ? `/${endpoint}?host_id=${hostId}` : `/${endpoint}`;
        const { data } = await apiClient.get<MetricResponse<L, H>>(url);
        return data;
      },
      refetchInterval: poll ? 5000 : false,
      staleTime: poll ? 1000 : Infinity,
      refetchOnWindowFocus: poll,
    });
  };
}
