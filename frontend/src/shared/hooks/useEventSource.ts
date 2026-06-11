import { useEffect } from 'react';
import { resetLiveMetrics, useMetricsStore } from '../lib/metricsStore';
import { recordGaugeFromEnvelope } from '../lib/hostGaugesStore';

export function useMetricsStream(hostId?: number | null) {
  const url = hostId ? `/api/v1/stream?host_id=${hostId}` : '/api/v1/stream';

  useEffect(() => {
    resetLiveMetrics();

    const es = new EventSource(url, { withCredentials: true });

    es.addEventListener('metrics', (e: MessageEvent) => {
      try {
        const data = JSON.parse(e.data) as Record<string, unknown> & { collecting_host_id?: number };
        // The stream carries EVERY host's envelopes; even when this page only
        // renders one host, keep the per-host cpu/ram gauges fresh for the
        // nested guest rows (no extra connection or REST traffic).
        recordGaugeFromEnvelope(data);
        const cid = data.collecting_host_id;
        if (hostId != null && cid !== undefined && Number(cid) !== Number(hostId)) {
          return;
        }
        const { collecting_host_id: _ignored, timestamp: tsRaw, ...rest } = data;
        let streamTimestamp: string | undefined;
        if (typeof tsRaw === 'string' && tsRaw.length > 0) {
          streamTimestamp = tsRaw;
        } else if (typeof tsRaw === 'number' && Number.isFinite(tsRaw)) {
          streamTimestamp = new Date(tsRaw).toISOString();
        }
        useMetricsStore.getState().setMetrics({
          streamTimestamp,
          cpu: rest.cpu as Record<string, unknown> | undefined,
          memory: rest.memory as Record<string, unknown> | undefined,
          disk: rest.disk as Record<string, unknown> | undefined,
          network: rest.network as Record<string, unknown> | undefined,
          docker: rest.docker as Record<string, unknown> | undefined,
          applications: Array.isArray(rest.applications) ? (rest.applications as unknown[]) : undefined,
        });
      } catch {
        // ignore malformed messages
      }
    });

    es.onerror = () => {
      // browser reconnects automatically via EventSource spec
    };

    return () => {
      es.close();
      resetLiveMetrics();
    };
  }, [url]);
}

/**
 * Gauges-only stream subscription for pages without a metrics stream of their
 * own (the machine list): one EventSource feeding the per-host cpu/ram gauges
 * store. Pages that already mount useMetricsStream get this for free.
 */
export function useHostGaugesStream() {
  useEffect(() => {
    const es = new EventSource('/api/v1/stream', { withCredentials: true });
    es.addEventListener('metrics', (e: MessageEvent) => {
      try {
        recordGaugeFromEnvelope(JSON.parse(e.data) as Record<string, unknown>);
      } catch {
        // ignore malformed messages
      }
    });
    es.onerror = () => {
      // browser reconnects automatically via EventSource spec
    };
    return () => es.close();
  }, []);
}
