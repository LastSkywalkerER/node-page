import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'

export interface RaftPeer {
  id: string
  addr: string
  suffrage: 'voter' | 'nonvoter'
}

export interface RaftStatus {
  enabled: boolean
  cluster_id?: string
  node_id?: string
  state?: string
  leader_id?: string
  leader_addr?: string
  term?: number
  last_index?: number
  applied_index?: number
  commit_index?: number
  peers?: RaftPeer[]
  advertise_url?: string
  bridge_enabled?: boolean
  bridge_started_at?: string
}

export interface BridgeSample {
  url: string
  cluster_id: string
  node_id: string
  rtt: number // nanoseconds
  last_ok?: string
  last_err?: string
  consecutive_ok: number
  healthy: boolean
}

export interface RaftStatusResponse {
  status: RaftStatus
  bridge_samples?: BridgeSample[]
  /** Set when the most recent boot-time activation failed (e.g. port in use). */
  boot_error?: string
}

/**
 * Polls the local node's Raft view every 5s. Safe to call on
 * Raft-disabled deployments — backend returns Enabled=false and
 * bridge_samples=null.
 */
export function useRaftStatus(enabled: boolean = true) {
  return useQuery<RaftStatusResponse>({
    queryKey: ['raft', 'status'],
    queryFn: async () => {
      const resp = await apiClient.get<RaftStatusResponse>('/raft/status')
      return resp.data
    },
    refetchInterval: 5000,
    refetchOnWindowFocus: false,
    enabled,
    retry: false,
  })
}

export interface IssueJoinTokenResponse {
  token: string
  expires_at: string
  cluster_id?: string
}

export function useIssueRaftJoinToken() {
  const queryClient = useQueryClient()
  return useMutation<IssueJoinTokenResponse, Error, { ttl_minutes?: number }>({
    mutationFn: async (input) => {
      const resp = await apiClient.post<IssueJoinTokenResponse>('/raft/join-token', input)
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}

export function useAddRaftPeer() {
  const queryClient = useQueryClient()
  return useMutation<{ added: string }, Error, { id: string; addr: string }>({
    mutationFn: async (input) => {
      const resp = await apiClient.post<{ added: string }>('/raft/peers', input)
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}

export function useRemoveRaftPeer() {
  const queryClient = useQueryClient()
  return useMutation<{ removed: string }, Error, string>({
    mutationFn: async (id) => {
      const resp = await apiClient.delete<{ removed: string }>(`/raft/peers/${encodeURIComponent(id)}`)
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}

export interface SaveBridgeConfigInput {
  shared_secret: string
  remote_seeds: string[]
  advertise_url?: string
}

/**
 * Hot-updates the cross-cluster bridge configuration on the running
 * server. Rebuilds the sender/picker/receiver in place and persists the
 * values into .env so the change survives a restart.
 */
export function useSaveRaftBridge() {
  const queryClient = useQueryClient()
  return useMutation<{ saved: boolean }, Error, SaveBridgeConfigInput>({
    mutationFn: async (input) => {
      const resp = await apiClient.post<{ saved: boolean }>('/raft/bridge', input)
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}

/**
 * Wipes RAFT_* settings from .env so the next restart boots with Raft
 * disabled. Used to recover from a stuck boot-time activation (e.g.
 * port already in use forever) without editing .env by hand.
 */
export function useResetRaftConfig() {
  const queryClient = useQueryClient()
  return useMutation<{ reset: boolean; next: string }, Error, void>({
    mutationFn: async () => {
      const resp = await apiClient.post<{ reset: boolean; next: string }>('/raft/reset', {})
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}
