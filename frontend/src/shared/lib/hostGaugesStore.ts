import { create } from 'zustand';

/**
 * Per-host live gauges fed from the SSE stream. The stream broadcasts every
 * host's snapshot (`collecting_host_id` envelopes) — one open connection per
 * page is enough to keep cpu/ram for ALL machines fresh, with zero extra REST
 * traffic. Consumed by the minimized guest rows nested in hypervisor cards.
 */
export interface HostGauge {
  cpuPct: number | null;
  memPct: number | null;
  /** Date.now() of the last envelope — lets consumers ignore stale values. */
  at: number;
}

interface HostGaugesState {
  gauges: Record<number, HostGauge>;
  setGauge: (hostId: number, gauge: HostGauge) => void;
}

export const useHostGaugesStore = create<HostGaugesState>((set) => ({
  gauges: {},
  setGauge: (hostId, gauge) =>
    set((state) => ({ gauges: { ...state.gauges, [hostId]: gauge } })),
}));

/** Extracts cpu/ram usage from a raw SSE envelope into the gauges store. */
export function recordGaugeFromEnvelope(data: Record<string, unknown>) {
  const cid = Number((data as { collecting_host_id?: unknown }).collecting_host_id);
  if (!Number.isFinite(cid) || cid <= 0) return;
  const cpu = data.cpu as { usage_percent?: unknown } | undefined;
  const mem = data.memory as { usage_percent?: unknown } | undefined;
  const cpuPct = typeof cpu?.usage_percent === 'number' ? cpu.usage_percent : null;
  const memPct = typeof mem?.usage_percent === 'number' ? mem.usage_percent : null;
  if (cpuPct == null && memPct == null) return;
  useHostGaugesStore.getState().setGauge(cid, { cpuPct, memPct, at: Date.now() });
}
