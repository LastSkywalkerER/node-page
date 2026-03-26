import { useEffect } from 'react';
import type { QueryClient } from '@tanstack/react-query';
import { useQueryClient } from '@tanstack/react-query';
import { useMetricsStore } from '../lib/metricsStore';

const MAX_LIVE_HISTORY = 240;

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

function trimHistory<T>(history: T[]): T[] {
  if (history.length <= MAX_LIVE_HISTORY) return history;
  return history.slice(-MAX_LIVE_HISTORY);
}

function appendHistoryPoint<T extends { timestamp: string }>(history: T[], point: T | null): T[] {
  if (!point) return history;
  const last = history[history.length - 1];
  if (last && last.timestamp === point.timestamp) {
    const copy = history.slice(0, -1) as T[];
    copy.push(point);
    return trimHistory(copy);
  }
  return trimHistory([...history, point]);
}

function num(v: unknown, fallback = 0): number {
  return typeof v === 'number' && !Number.isNaN(v) ? v : fallback;
}

function buildCpuHistoryPoint(partial: Record<string, unknown>, ts: string): Record<string, unknown> | null {
  const usage = partial.usage_percent;
  if (typeof usage !== 'number' || Number.isNaN(usage)) return null;
  return {
    timestamp: ts,
    usage,
    load_avg_1: num(partial.load_avg_1),
    load_avg_5: num(partial.load_avg_5),
    load_avg_15: num(partial.load_avg_15),
    temperature: num(partial.temperature),
  };
}

function buildMemoryHistoryPoint(partial: Record<string, unknown>, ts: string): Record<string, unknown> | null {
  const pct = partial.usage_percent;
  if (typeof pct !== 'number' || Number.isNaN(pct)) return null;
  return {
    timestamp: ts,
    usage_percent: pct,
    used_bytes: num(partial.used),
    total_bytes: num(partial.total),
  };
}

function buildDiskHistoryPoint(partial: Record<string, unknown>, ts: string): Record<string, unknown> | null {
  const pct = partial.usage_percent;
  if (typeof pct !== 'number' || Number.isNaN(pct)) return null;
  return {
    timestamp: ts,
    usage_percent: pct,
    used_bytes: num(partial.used),
    total_bytes: num(partial.total),
  };
}

function buildNetworkHistoryPoint(partial: Record<string, unknown>, ts: string): Record<string, unknown> | null {
  const ifaces = partial.interfaces;
  if (!Array.isArray(ifaces)) return null;
  return {
    timestamp: ts,
    interfaces: ifaces,
  };
}

function mergeLatestIntoQuery(
  qc: QueryClient,
  queryKeyPrefix: string,
  hostId: number,
  partial: Record<string, unknown> | undefined,
  streamTs: string | undefined,
  buildPoint: ((p: Record<string, unknown>, ts: string) => Record<string, unknown> | null) | null
) {
  if (partial == null || typeof partial !== 'object') return;
  qc.setQueryData([queryKeyPrefix, hostId], (old: unknown) => {
    const o = (old ?? {}) as Record<string, unknown>;
    const prev = o.latest as Record<string, unknown> | null | undefined;
    if (queryKeyPrefix === 'docker-metrics' && shouldIgnoreEmptyDockerMerge(prev, partial)) {
      return o;
    }
    const baseHistory = Array.isArray(o.history) ? (o.history as { timestamp: string }[]) : [];
    let history: { timestamp: string }[] = baseHistory;
    if (streamTs && buildPoint) {
      const pt = buildPoint(partial, streamTs);
      if (pt && typeof pt === 'object' && 'timestamp' in pt && typeof (pt as { timestamp: unknown }).timestamp === 'string') {
        history = appendHistoryPoint([...baseHistory], pt as { timestamp: string });
      }
    }
    const nextLatest =
      prev != null && typeof prev === 'object' ? { ...prev, ...partial } : { ...partial };
    return { ...o, latest: nextLatest, history };
  });
}

/**
 * Pushes SSE live snapshots from metricsStore into React Query caches so widgets
 * keep using useCPU/useMemory/... without polling, and chart `history` grows from the stream.
 */
export function useLiveMetricsQuerySync(hostId: number) {
  const qc = useQueryClient();

  useEffect(() => {
    return useMetricsStore.subscribe((state) => {
      const streamTs = state.streamTimestamp;
      mergeLatestIntoQuery(qc, 'cpu-metrics', hostId, state.cpu, streamTs, buildCpuHistoryPoint);
      mergeLatestIntoQuery(qc, 'memory-metrics', hostId, state.memory, streamTs, buildMemoryHistoryPoint);
      mergeLatestIntoQuery(qc, 'disk-metrics', hostId, state.disk, streamTs, buildDiskHistoryPoint);
      mergeLatestIntoQuery(qc, 'network-metrics', hostId, state.network, streamTs, buildNetworkHistoryPoint);
      mergeLatestIntoQuery(qc, 'docker-metrics', hostId, state.docker, streamTs, null);
    });
  }, [hostId, qc]);
}
