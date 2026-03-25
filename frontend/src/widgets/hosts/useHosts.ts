import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';
import type { HostsResponse } from './schemas';

export function useHosts() {
  return useQuery({
    queryKey: ['hosts'],
    queryFn: async () => {
      const { data } = await apiClient.get<HostsResponse>('/hosts');
      return data;
    },
    refetchInterval: 30000,
    staleTime: 10000,
  });
}
