import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { apiClient } from '../../shared/lib/api';

export interface ConnectionStatus {
  isConnected: boolean;
  latency: number | null;
  uptime: string | null;
}

// Hook for monitoring connection status
export function useConnectionStatus(hostId?: number) {
  const refetchRef = useRef<() => void>();

  const query = useQuery({
    queryKey: ['connection-status-widget', hostId],
    queryFn: async ({ queryKey }: any): Promise<{ status: string; uptime?: string; latency_ms?: number; host_uptime_seconds?: number; latency?: number; uptime_formatted?: string }> => {
      const [, currentHostId] = queryKey;
      const startTime = Date.now();
      const url = currentHostId ? `/health?host_id=${currentHostId}` : '/health';
      const { data } = await apiClient.get(url);
      const endTime = Date.now();

      // Use server-provided latency for host health checks, otherwise measure locally
      let latency: number;
      if (currentHostId && data.latency_ms !== undefined) {
        latency = data.latency_ms;
      } else {
        // Measure latency
        latency = endTime - startTime;
      }

      return {
        ...data,
        latency,
        uptime_formatted: data.uptime
      };
    },
    staleTime: 1000,
    retry: 3,
    retryDelay: 1000,
    enabled: true,
  });

  // Update refetch ref
  refetchRef.current = query.refetch;

  // Manual refetch every second
  useEffect(() => {
    const interval = setInterval(() => {
      refetchRef.current?.();
    }, 1000);

    return () => clearInterval(interval);
  }, []); // Empty dependency array to run only once

  return {
    isConnected: query.isSuccess,
    latency: query.data?.latency ?? null,
    uptime: query.data?.uptime_formatted ?? null,
    isLoading: query.isLoading,
    error: query.error,
  };
}
