import { useQuery } from '@tanstack/react-query';

// Hook for Disk metrics - updates every 5 seconds
export function useDisk() {
  return useQuery({
    queryKey: ['disk-metrics'],
    queryFn: async () => {
      const response = await fetch('/api/disk');
      if (!response.ok) {
        throw new Error('Failed to fetch disk metrics');
      }
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
