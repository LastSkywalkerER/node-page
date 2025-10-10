import { useQuery } from '@tanstack/react-query';

// Hook for Network metrics - updates every 5 seconds
export function useNetwork(hostId?: number | null) {
  const queryKey = hostId ? ['network-metrics', hostId] : ['network-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/api/network?host_id=${hostId}` : '/api/network';
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch network metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
