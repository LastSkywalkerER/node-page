import { useQuery } from '@tanstack/react-query';

// Hook for Disk metrics - updates every 5 seconds
export function useDisk(hostId?: number | null) {
  const queryKey = hostId ? ['disk-metrics', hostId] : ['disk-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/api/disk?host_id=${hostId}` : '/api/disk';
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch disk metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
