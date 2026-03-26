import { useEffect } from 'react';
import type { QueryClient } from '@tanstack/react-query';
import { useQueryClient } from '@tanstack/react-query';
import { useMetricsStore } from '../lib/metricsStore';

/**
 * Avoid replacing a rich Docker REST payload with an empty live tick (ping OK but list/inspect failed, or race).
 */
function shouldIgnoreEmptyDockerMerge(
  prev: Record<string, unknown> | null | undefined,
  partial: Record<string, unknown>
): boolean {
  const prevStacks = prev?.stacks;
  const nextStacks = partial.stacks;
  if (!Array.isArray(prevStacks) || prevStacks.length === 0) return false;
  if (!Array.isArray(nextStacks) || nextStacks.length > 0) return false;
  const nextTotal = partial.total_containers;
  if (partial.docker_available === true && (nextTotal === 0 || nextTotal === undefined)) {
    return true;
  }
  return false;
}

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
    if (queryKeyPrefix === 'docker-metrics' && shouldIgnoreEmptyDockerMerge(prev, partial)) {
      return o;
    }
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
