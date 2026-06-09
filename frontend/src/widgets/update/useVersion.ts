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
