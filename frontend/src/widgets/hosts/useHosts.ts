import { useQuery } from '@tanstack/react-query';

// Hook for getting all hosts
export function useHosts() {
  return useQuery({
    queryKey: ['hosts'],
    queryFn: async () => {
      const response = await fetch('/api/hosts');
      if (!response.ok) {
        throw new Error('Failed to fetch hosts');
      }
      return response.json();
    },
    refetchInterval: 30000, // Refresh every 30 seconds
    staleTime: 10000,
  });
}
