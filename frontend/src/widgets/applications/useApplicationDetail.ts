import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/shared/lib/api';
import type {
  DockerApplication,
  ComposeView,
  ApplicationLogsResponse,
} from './schemas';

/** Single application detail (containers + summed resources), polled live. */
export function useApplicationDetail(hostId: number, project: string) {
  return useQuery<{ application: DockerApplication }>({
    queryKey: ['application-detail', hostId, project],
    queryFn: async () => {
      const { data } = await apiClient.get<{ application: DockerApplication }>(
        `/applications/${encodeURIComponent(project)}?host_id=${hostId}`
      );
      return data;
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}

/** Application composition: synthetic compose + real YAML when reachable. */
export function useApplicationCompose(hostId: number, project: string, enabled = true) {
  return useQuery<ComposeView>({
    queryKey: ['application-compose', hostId, project],
    queryFn: async () => {
      const { data } = await apiClient.get<ComposeView>(
        `/applications/${encodeURIComponent(project)}/compose?host_id=${hostId}`
      );
      return data;
    },
    enabled,
    staleTime: 30_000,
  });
}

/** Recent container logs (local node only; remote returns available=false). */
export function useApplicationLogs(
  hostId: number,
  project: string,
  container: string | undefined,
  opts?: { enabled?: boolean; tail?: number }
) {
  const tail = opts?.tail ?? 500;
  return useQuery<ApplicationLogsResponse>({
    queryKey: ['application-logs', hostId, project, container ?? '', tail],
    queryFn: async () => {
      const params = new URLSearchParams({ host_id: String(hostId), tail: String(tail) });
      if (container) params.set('container', container);
      const { data } = await apiClient.get<ApplicationLogsResponse>(
        `/applications/${encodeURIComponent(project)}/logs?${params.toString()}`
      );
      return data;
    },
    enabled: opts?.enabled ?? true,
    refetchInterval: 5000,
    staleTime: 0,
  });
}
