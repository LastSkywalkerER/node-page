import { useEffect } from 'react';
import { resetLiveMetrics, useMetricsStore } from '../lib/metricsStore';

export function useMetricsStream(hostId?: number | null) {
  const url = hostId ? `/api/v1/stream?host_id=${hostId}` : '/api/v1/stream';

  useEffect(() => {
    resetLiveMetrics();

    const es = new EventSource(url, { withCredentials: true });

    es.addEventListener('metrics', (e: MessageEvent) => {
      try {
        const data = JSON.parse(e.data) as Record<string, unknown> & { collecting_host_id?: number };
        const cid = data.collecting_host_id;
        if (hostId != null && cid !== undefined && Number(cid) !== Number(hostId)) {
          return;
        }
        const { collecting_host_id: _ignored, timestamp: _ts, ...rest } = data;
        useMetricsStore.getState().setMetrics({
          cpu: rest.cpu as Record<string, unknown> | undefined,
          memory: rest.memory as Record<string, unknown> | undefined,
          disk: rest.disk as Record<string, unknown> | undefined,
          network: rest.network as Record<string, unknown> | undefined,
          docker: rest.docker as Record<string, unknown> | undefined,
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
