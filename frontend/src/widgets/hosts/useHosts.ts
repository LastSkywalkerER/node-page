import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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

/**
 * Removes a registered host and all of its metrics. Cluster-wide (replicated by
 * MAC) when Raft is enabled; local otherwise. The local collector (id=1) is
 * rejected by the backend — use the Raft "leave cluster" flow for this node.
 */
export function useDeleteHost() {
  const queryClient = useQueryClient();
  return useMutation<{ removed: number }, Error, number>({
    mutationFn: async (id) => {
      const { data } = await apiClient.delete<{ removed: number }>(`/hosts/${id}`);
      return data;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['hosts'] });
    },
  });
}
