import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

export interface ConnectionStatus {
  isConnected: boolean;
  latency: number | null;
  uptime: string | null;
  /** Show the uptime row only for the local collector host (id=1): /health
   *  returns THIS server's process uptime, which is meaningful for its own
   *  machine but not for remote Raft peers. */
  showUptime: boolean;
}

interface HealthPayload {
  status: string;
  uptime?: string;
  latency_ms?: number;
  host_uptime_seconds?: number;
  last_seen?: string;
}

type ConnectionStatusQueryResult = HealthPayload & {
  latency: number;
  uptime_display: string | null;
  show_uptime: boolean;
};

// Hook for monitoring connection status (uses /health host-specific semantics).
export function useConnectionStatus(hostId?: number) {
  const query = useQuery({
    queryKey: ['connection-status-widget', hostId],
    queryFn: async ({ queryKey }: { queryKey: unknown[] }): Promise<ConnectionStatusQueryResult> => {
      const [, currentHostId] = queryKey;
      const startTime = Date.now();
      const url = currentHostId ? `/health?host_id=${currentHostId}` : '/health';
      const { data } = await apiClient.get<HealthPayload>(url);
      const endTime = Date.now();

      let latency: number;
      if (currentHostId && data.latency_ms !== undefined) {
        latency = data.latency_ms;
      } else {
        latency = endTime - startTime;
      }

      // /health returns this server's process uptime, which is only meaningful
      // for its own machine — the local collector row (id=1). Remote Raft peers
      // (id>=2) have no verifiable process uptime here, so hide the row.
      const showUptime = !currentHostId || currentHostId === 1;
      const uptimeDisplay = showUptime ? (data.uptime ?? null) : null;

      return {
        ...data,
        latency,
        uptime_display: uptimeDisplay,
        show_uptime: showUptime,
      };
    },
    staleTime: 5000,
    refetchInterval: 5000,
    retry: 3,
    retryDelay: 1000,
    enabled: true,
  });

  const payload = query.data;

  return {
    isConnected: payload?.status === 'online',
    latency: payload?.latency ?? null,
    uptime: payload?.uptime_display ?? null,
    showUptime: payload?.show_uptime ?? true,
    isLoading: query.isLoading,
    error: query.error,
  };
}
