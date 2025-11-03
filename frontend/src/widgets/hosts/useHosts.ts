import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';

// Hook for getting all hosts
export function useHosts() {
  return useQuery({
    queryKey: ['hosts'],
    queryFn: async () => {
      const { data } = await apiClient.get('/hosts');
      return data;
    },
    refetchInterval: 30000, // Refresh every 30 seconds
    staleTime: 10000,
  });
}
