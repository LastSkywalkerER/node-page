import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { PBSStatusSchema, type PBSStatus } from './schemas'

/**
 * Proxmox Backup Server datastore + backup detail for a host.
 * Served by the polling node (leader); enable only for PBS hosts.
 */
export function usePBS(hostId: number, enabled = true) {
  return useQuery<PBSStatus>({
    queryKey: ['pbs', hostId],
    queryFn: async () => {
      const { data } = await apiClient.get(`/pbs?host_id=${hostId}`)
      return PBSStatusSchema.parse(data)
    },
    enabled,
    refetchInterval: 15000,
    staleTime: 10000,
  })
}
