import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '@/shared/lib/api';

export interface VersionInfo {
  current: string;
  commit?: string;
  built_at?: string;
  deployment?: string; // docker | native
  channel?: string;
  latest?: string;
  update_available?: boolean;
  auto_update?: boolean;
  checked_at?: string;
  managed_externally?: boolean;
}

/** Public build/version + update state (GET /version). Polled hourly. */
export function useVersion() {
  return useQuery<VersionInfo>({
    queryKey: ['version'],
    queryFn: async () => {
      const r = await apiClient.get<{ data: VersionInfo }>('/version');
      return r.data.data;
    },
    refetchInterval: 60 * 60 * 1000,
    staleTime: 30 * 60 * 1000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/**
 * Force a fresh release check (GET /version?refresh=1, rate-limited server-side)
 * and sync the result into the version cache. Used when the update popup opens
 * so the operator sees current state instead of the hourly cache.
 */
export function useRefreshVersion() {
  const qc = useQueryClient();
  return useMutation<VersionInfo, Error, void>({
    mutationFn: async () => {
      const r = await apiClient.get<{ data: VersionInfo }>('/version?refresh=1');
      return r.data.data;
    },
    onSuccess: (data) => qc.setQueryData(['version'], data),
  });
}

/** Toggle auto-update (admin). Persists server-side; returns the new version state. */
export function useSetAutoUpdate() {
  const qc = useQueryClient();
  return useMutation<VersionInfo, Error, boolean>({
    mutationFn: async (enabled) => {
      const r = await apiClient.post<{ data: VersionInfo }>('/settings/auto-update', { enabled });
      return r.data.data;
    },
    onSuccess: (data) => qc.setQueryData(['version'], data),
  });
}

/** Trigger an immediate update (admin). Docker → controller pull+recreate. */
export function useUpdateNow() {
  return useMutation<{ message: string }, Error, void>({
    mutationFn: async () => {
      const r = await apiClient.post<{ data: { message: string } }>('/settings/update-now', {});
      return r.data.data;
    },
  });
}
