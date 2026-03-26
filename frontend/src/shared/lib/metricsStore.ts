import { create } from 'zustand';

// Live snapshot keys match CollectAllCurrent /api JSON (partial merge into REST `latest`).
export interface MetricsPayload {
  cpu?: Record<string, unknown>;
  memory?: Record<string, unknown>;
  disk?: Record<string, unknown>;
  network?: Record<string, unknown>;
  docker?: Record<string, unknown>;
}

interface MetricsState extends MetricsPayload {
  setMetrics: (data: MetricsPayload) => void;
}

export const useMetricsStore = create<MetricsState>((set) => ({
  cpu: undefined,
  memory: undefined,
  disk: undefined,
  network: undefined,
  docker: undefined,
  // Do not assign undefined — a partial SSE envelope must not wipe keys that were omitted from JSON.
  setMetrics: (data) =>
    set((state) => {
      const next = { ...state } as MetricsState;
      (['cpu', 'memory', 'disk', 'network', 'docker'] as const).forEach((k) => {
        if (k in data && data[k] !== undefined) {
          next[k] = data[k] as never;
        }
      });
      return next;
    }),
}));

/** Clears live SSE snapshot (call when switching machine or reconnecting stream). */
export function resetLiveMetrics() {
  useMetricsStore.setState({
    cpu: undefined,
    memory: undefined,
    disk: undefined,
    network: undefined,
    docker: undefined,
  });
}
