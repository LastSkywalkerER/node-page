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
  deploy_webhook_configured?: boolean;
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

/** Switch the release channel (admin): 'stable' | 'beta'. Returns new version state. */
export function useSetReleaseChannel() {
  const qc = useQueryClient();
  return useMutation<VersionInfo, Error, 'stable' | 'beta'>({
    mutationFn: async (channel) => {
      const r = await apiClient.post<{ data: VersionInfo }>('/settings/release-channel', { channel });
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

/**
 * Orchestrator deploy-webhook URL (admin; managed-externally deployments).
 * Fetched lazily — only while the update popup is open on such a deployment.
 */
export function useDeployWebhook(enabled: boolean) {
  return useQuery<{ url: string }>({
    queryKey: ['deploy-webhook'],
    queryFn: async () => {
      const r = await apiClient.get<{ data: { url: string } }>('/settings/deploy-webhook');
      return r.data.data;
    },
    enabled,
    staleTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/** Save (or clear with '') the deploy-webhook URL. Returns the new version state. */
export function useSetDeployWebhook() {
  const qc = useQueryClient();
  return useMutation<VersionInfo, Error, string>({
    mutationFn: async (url) => {
      const r = await apiClient.post<{ data: VersionInfo }>('/settings/deploy-webhook', { url });
      return r.data.data;
    },
    onSuccess: (data, url) => {
      qc.setQueryData(['version'], data);
      qc.setQueryData(['deploy-webhook'], { url });
    },
  });
}
