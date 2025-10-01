import { useQuery } from '@tanstack/react-query';

// Hook for Network metrics - updates every 5 seconds
export function useNetwork() {
  return useQuery({
    queryKey: ['network-metrics'],
    queryFn: async () => {
      const response = await fetch('/api/network');
      if (!response.ok) {
        throw new Error('Failed to fetch network metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
