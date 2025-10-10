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

// Hook for getting current host
export function useCurrentHost() {
  return useQuery({
    queryKey: ['current-host'],
    queryFn: async () => {
      const response = await fetch('/api/hosts/current');
      if (!response.ok) {
        throw new Error('Failed to fetch current host');
      }
      return response.json();
    },
    refetchInterval: 30000,
    staleTime: 10000,
  });
}

