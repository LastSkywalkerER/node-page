import { useQuery } from '@tanstack/react-query';

// Hook for Docker metrics - updates every 5 seconds
export function useDocker(hostId?: number | null) {
  const queryKey = hostId ? ['docker-metrics', hostId] : ['docker-metrics'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/api/docker?host_id=${hostId}` : '/api/docker';
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch Docker metrics');
      }
      const data = await response.json();
      console.log('Docker metrics:', data);
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
