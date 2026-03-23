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
  setMetrics: (data) => set((state) => ({ ...state, ...data })),
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
