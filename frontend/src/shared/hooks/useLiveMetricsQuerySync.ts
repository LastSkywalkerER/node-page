import { useEffect } from 'react';
import type { QueryClient } from '@tanstack/react-query';
import { useQueryClient } from '@tanstack/react-query';
import { useMetricsStore } from '../lib/metricsStore';

function mergeLatestIntoQuery(
  qc: QueryClient,
  queryKeyPrefix: string,
  hostId: number,
  partial: Record<string, unknown> | undefined
) {
  if (partial == null || typeof partial !== 'object') return;
  qc.setQueryData([queryKeyPrefix, hostId], (old: unknown) => {
    const o = (old ?? {}) as Record<string, unknown>;
    const history = Array.isArray(o.history) ? o.history : [];
    const prev = o.latest as Record<string, unknown> | null | undefined;
    const nextLatest =
      prev != null && typeof prev === 'object' ? { ...prev, ...partial } : { ...partial };
    return { ...o, latest: nextLatest, history };
  });
}

/**
 * Pushes SSE live snapshots from metricsStore into React Query caches so widgets
 * keep using useCPU/useMemory/... without polling.
 */
export function useLiveMetricsQuerySync(hostId: number) {
  const qc = useQueryClient();

  useEffect(() => {
    return useMetricsStore.subscribe((state) => {
      mergeLatestIntoQuery(qc, 'cpu-metrics', hostId, state.cpu);
      mergeLatestIntoQuery(qc, 'memory-metrics', hostId, state.memory);
      mergeLatestIntoQuery(qc, 'disk-metrics', hostId, state.disk);
      mergeLatestIntoQuery(qc, 'network-metrics', hostId, state.network);
      mergeLatestIntoQuery(qc, 'docker-metrics', hostId, state.docker);
    });
  }, [hostId, qc]);
}
