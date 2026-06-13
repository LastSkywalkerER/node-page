import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/shared/lib/api';
import type { ApplicationsResponse } from './schemas';

/**
 * Fetches applications for a single host (`/applications?host_id=`) or, when
 * hostId is omitted, all hosts aggregated (`/applications`, Phase 2).
 *
 * The aggregate `/applications` payload is heavy (docker inspect per app) — it
 * polls slowly (30s) since app liveness on the machine list doesn't need 5s
 * granularity. A per-host list ALSO receives live updates from SSE
 * (`syncApplications` in useLiveMetricsQuerySync) when a metrics stream is open,
 * so it can poll at the slower cadence too.
 */
export function useApplications(hostId?: number | null) {
  const queryKey = hostId != null ? ['applications', hostId] : ['applications', 'all'];

  return useQuery<ApplicationsResponse>({
    queryKey,
    queryFn: async () => {
      const url = hostId != null ? `/applications?host_id=${hostId}` : '/applications';
      const { data } = await apiClient.get<ApplicationsResponse>(url);
      return data;
    },
    refetchInterval: 30_000,
    staleTime: 1000,
  });
}
