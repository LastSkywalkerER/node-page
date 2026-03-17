import { useEffect } from 'react';
import { useMetricsStore } from '../lib/metricsStore';

export function useMetricsStream(hostId?: number | null) {
  const url = hostId ? `/api/v1/stream?host_id=${hostId}` : '/api/v1/stream';

  useEffect(() => {
    const es = new EventSource(url, { withCredentials: true });

    es.addEventListener('metrics', (e: MessageEvent) => {
      try {
        const data = JSON.parse(e.data);
        useMetricsStore.getState().setMetrics(data);
      } catch {
        // ignore malformed messages
      }
    });

    es.onerror = () => {
      // browser reconnects automatically via EventSource spec
    };

    return () => es.close();
  }, [url]);
}
