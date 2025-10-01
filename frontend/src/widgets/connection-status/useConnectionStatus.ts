import { useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';

export interface ConnectionStatus {
  isConnected: boolean;
  latency: number | null;
  uptime: string | null;
}

// Hook for monitoring connection status
export function useConnectionStatus() {
  const [latency, setLatency] = useState<number | null>(null);
  const [isConnected, setIsConnected] = useState(false);
  const [uptime, setUptime] = useState<string | null>(null);

  const query = useQuery({
    queryKey: ['connection-ping'],
    queryFn: async (): Promise<{ status: string; uptime?: string }> => {
      const startTime = Date.now();
      const response = await fetch('/api/health');
      const endTime = Date.now();

      if (!response.ok) {
        throw new Error('Connection failed');
      }

      // Measure latency
      const ping = endTime - startTime;
      setLatency(ping);

      const data = await response.json();
      setUptime(data.uptime || null);
      return data;
    },
    refetchInterval: 1000, // Check connection every 1 second
    staleTime: 1000,
    retry: 3,
    retryDelay: 1000,
  });

  // Update connection status based on query state
  useEffect(() => {
    setIsConnected(query.isSuccess);
    if (!query.isSuccess) {
      setLatency(null);
      setUptime(null);
    }
  }, [query.isSuccess]);

  return {
    isConnected,
    latency,
    uptime,
    isLoading: query.isLoading,
    error: query.error,
  };
}
