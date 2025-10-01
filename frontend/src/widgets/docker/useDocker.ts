import { useQuery } from '@tanstack/react-query';

// Hook for Docker metrics - updates every 5 seconds
export function useDocker() {
  return useQuery({
    queryKey: ['docker-metrics'],
    queryFn: async () => {
      const response = await fetch('/api/docker');
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
