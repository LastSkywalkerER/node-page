import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { resetLiveMetrics, useMetricsStore } from '../lib/metricsStore';
import { recordGaugeFromEnvelope } from '../lib/hostGaugesStore';
import { applyEnvelopeToMetricCaches } from './useLiveMetricsQuerySync';

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
 * All-hosts stream subscription for the machine list: one EventSource that feeds
 * BOTH the per-host cpu/ram gauges store (nested guest rows) AND every host's
 * cpu/memory/disk/network metric query caches (keyed by `collecting_host_id`),
 * so the live cards update from SSE instead of polling each host every 5s.
 *
 * Returns whether the stream is currently connected — the list page keeps a slow
 * fallback poll only while SSE is down (or for hosts the stream never carries).
 */
export function useHostGaugesStream(): { connected: boolean } {
  const qc = useQueryClient();
  const [connected, setConnected] = useState(false);
  // Avoid re-subscribing on every render; the query client is stable per app.
  const qcRef = useRef(qc);
  qcRef.current = qc;

  useEffect(() => {
    const es = new EventSource('/api/v1/stream', { withCredentials: true });
    es.addEventListener('metrics', (e: MessageEvent) => {
      try {
        const data = JSON.parse(e.data) as Record<string, unknown> & {
          collecting_host_id?: number;
          timestamp?: unknown;
        };
        recordGaugeFromEnvelope(data);
        applyEnvelopeToMetricCaches(qcRef.current, data);
        setConnected(true);
      } catch {
        // ignore malformed messages
      }
    });
    es.onopen = () => setConnected(true);
    es.onerror = () => {
      // browser reconnects automatically via EventSource spec; treat as down so
      // the list page falls back to a slow poll until messages resume.
      setConnected(false);
    };
    return () => {
      es.close();
      setConnected(false);
    };
  }, []);

  return { connected };
}
