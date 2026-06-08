import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/shared/lib/api';
import type { ApplicationsResponse } from './schemas';

/**
 * Fetches applications for a single host (`/applications?host_id=`) or, when
 * hostId is omitted, all hosts aggregated (`/applications`, Phase 2).
 * Polls every 5s (like health); SSE-driven liveness can replace this later.
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
    refetchInterval: 5000,
    staleTime: 1000,
  });
}
