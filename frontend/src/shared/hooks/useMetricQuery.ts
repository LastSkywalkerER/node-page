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
    options?: { mode?: MetricFetchMode; pollMs?: number }
  ) {
    // One REST load on mount for every host; live updates arrive over SSE
    // (useMetricsStream / useLiveMetricsQuerySync) for the local host AND every
    // replicated peer, so the frontend treats all hosts uniformly. An explicit
    // 'poll' mode still works for the rare legacy caller that wants interval refetch
    // (pollMs overrides the 5s default — e.g. a slow SSE-disconnect fallback).
    const mode = options?.mode ?? 'snapshot';
    const poll = mode === 'poll';
    const pollMs = options?.pollMs ?? 5000;
    const queryKey = hostId != null ? [queryKeyBase, hostId] : [queryKeyBase];

    return useQuery<MetricResponse<L, H>>({
      queryKey,
      queryFn: async () => {
        const url = hostId != null ? `/${endpoint}?host_id=${hostId}` : `/${endpoint}`;
        const { data } = await apiClient.get<MetricResponse<L, H>>(url);
        return data;
      },
      refetchInterval: poll ? pollMs : false,
      staleTime: poll ? 1000 : Infinity,
      refetchOnWindowFocus: poll,
    });
  };
}
