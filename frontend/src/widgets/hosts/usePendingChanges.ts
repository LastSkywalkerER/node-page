import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';
import { useUserStore } from '../../shared/store/user';
import { PendingChangesResponseSchema, type HostPendingChange } from './schemas';

/**
 * Connector-proposed identity updates (rename / MAC change) frozen for admin
 * approval. Admin-only endpoint — the query is disabled for other roles.
 */
export function usePendingChanges() {
  const isAdmin = useUserStore((s) => s.user?.role === 'ADMIN');
  return useQuery<HostPendingChange[]>({
    queryKey: ['hosts', 'pending-changes'],
    queryFn: async () => {
      const { data } = await apiClient.get('/hosts/pending-changes');
      return PendingChangesResponseSchema.parse(data).changes;
    },
    enabled: isAdmin,
    refetchInterval: 30000,
    staleTime: 10000,
  });
}

function usePendingChangeAction(action: 'approve' | 'reject') {
  const queryClient = useQueryClient();
  return useMutation<unknown, Error, string>({
    mutationFn: async (changeId) => {
      const { data } = await apiClient.post(`/hosts/pending-changes/${changeId}/${action}`);
      return data;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['hosts'] });
    },
  });
}

/** Applies the proposal onto the host row (cluster-wide when Raft is on). */
export function useApprovePendingChange() {
  return usePendingChangeAction('approve');
}

/** Marks the proposal rejected — the connector stops re-proposing that value. */
export function useRejectPendingChange() {
  return usePendingChangeAction('reject');
}
