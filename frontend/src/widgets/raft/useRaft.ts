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

export interface PeerURL {
  cluster_id: string
  node_id: string
  url: string
}

export interface BridgeSenderInfo {
  last_ship_at?: string
  last_ship_err?: string
  shipped_total: number
  pending: number
  uplink_only: boolean
}

export interface BridgeInfo {
  mode: 'push' | 'receive' | 'both'
  sender?: BridgeSenderInfo
  receiving: boolean
}

export interface UplinkInfo {
  cluster_id: string
  last_origin_index: number
  last_applied_at: string
}

export interface RaftStatusResponse {
  status: RaftStatus
  bridge_samples?: BridgeSample[]
  /** Bridge runtime state: mode + sender ship health. */
  bridge?: BridgeInfo
  /** Hub side: spoke clusters that have shipped entries here. */
  uplinks?: UplinkInfo[]
  /** Set when the most recent boot-time activation failed (e.g. port in use). */
  boot_error?: string
  /** Raw hashicorp/raft Stats() map — string-to-string. Used by the
   *  admin UI to surface low-level fields like last_contact, num_peers,
   *  latest_configuration when the cluster can't elect a leader. */
  raft_stats?: Record<string, string>
  peer_urls?: PeerURL[]
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

export interface StartClusterInput {
  cluster_id?: string
  node_id?: string
  bind_addr?: string
  advertise_addr?: string
  advertise_url?: string
}

export interface StartClusterResponse {
  data: {
    message: string
    cluster_id: string
    node_id: string
    advertise_addr: string
  }
}

/**
 * Bootstraps THIS already-provisioned standalone node into the leader of a
 * new single-voter cluster, at runtime (no restart). Non-destructive — the
 * node keeps its local data. POST /cluster/start.
 */
export function useStartCluster() {
  const queryClient = useQueryClient()
  return useMutation<StartClusterResponse, Error, StartClusterInput>({
    mutationFn: async (input) => {
      const resp = await apiClient.post<StartClusterResponse>('/cluster/start', input)
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
      queryClient.invalidateQueries({ queryKey: ['hosts'] })
    },
  })
}

export interface JoinClusterInput {
  peer_url: string
  token: string
  advertise_addr?: string
  advertise_url?: string
  acknowledge_data_loss: boolean
}

/**
 * Attaches THIS already-provisioned node to an existing cluster.
 * DESTRUCTIVE: the incoming snapshot replaces all local data (users,
 * metrics, settings) and the current admin is logged out. POST /cluster/join.
 */
export function useJoinCluster() {
  const queryClient = useQueryClient()
  return useMutation<{ data: { message: string; cluster_id: string } }, Error, JoinClusterInput>({
    mutationFn: async (input) => {
      const resp = await apiClient.post<{ data: { message: string; cluster_id: string } }>(
        '/cluster/join',
        input,
      )
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
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

/**
 * Self-leave: removes THIS node from the Raft cluster (the leader performs the
 * membership change) and decouples it to standalone — Raft stopped, on-disk
 * state wiped, RAFT_* removed from .env. The node keeps running standalone.
 */
export function useLeaveRaftCluster() {
  const queryClient = useQueryClient()
  return useMutation<{ left: boolean; node_id: string; next: string }, Error, void>({
    mutationFn: async () => {
      const resp = await apiClient.post<{ left: boolean; node_id: string; next: string }>('/raft/leave')
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
      queryClient.invalidateQueries({ queryKey: ['hosts'] })
    },
  })
}

export interface SaveBridgeConfigInput {
  shared_secret: string
  remote_seeds: string[]
  advertise_url?: string
  /** push (spoke uplink) | receive (hub) | both (legacy pair). */
  mode?: string
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

/**
 * Shuts the local Raft node down, deletes its BoltDB log + snapshot
 * files and re-activates as a fresh single-voter cluster. Used to
 * recover from a wedged cluster (e.g. a peer was added with an
 * unreachable advertise address and quorum is unreachable). Replicated
 * SQLite data (users, hosts, metrics) is kept intact.
 */
export function useWipeRaftState() {
  const queryClient = useQueryClient()
  return useMutation<{ wiped: boolean; next: string }, Error, void>({
    mutationFn: async () => {
      const resp = await apiClient.post<{ wiped: boolean; next: string }>('/raft/wipe-state', {})
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}

/**
 * Full reset — wipes data/raft AND removes RAFT_* from .env so the next
 * restart comes up Raft-disabled. SQLite tables (users, hosts, metrics)
 * are preserved. Apply on EVERY node when the cluster is wedged and you
 * want to start from scratch via the setup wizard.
 */
export function useFactoryResetRaft() {
  const queryClient = useQueryClient()
  return useMutation<{ reset: boolean; next: string }, Error, void>({
    mutationFn: async () => {
      const resp = await apiClient.post<{ reset: boolean; next: string }>('/raft/factory-reset', {})
      return resp.data
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['raft', 'status'] })
    },
  })
}

/**
 * TCP-probes a voter's advertise address from THIS server. Used by the
 * Voters panel to diagnose unreachable peers when the cluster can't
 * elect a leader.
 */
export interface ProbeVoterResult {
  reachable: boolean
  addr: string
  error?: string
}
export function useProbeVoter() {
  return useMutation<ProbeVoterResult, Error, string>({
    mutationFn: async (addr) => {
      const resp = await apiClient.post<ProbeVoterResult>('/raft/probe-voter', { addr })
      return resp.data
    },
  })
}

export interface AdvertiseCandidate {
  url: string
  reachable: boolean
  error?: string
}

export interface AdvertiseHints {
  ipv4: string
  raft_addr: string
  candidates: AdvertiseCandidate[]
}

/**
 * Server-side suggestions for the cluster-sync forms: the detected LAN IP,
 * the derived Raft advertise address, and advertise-URL candidates TCP-probed
 * from the node itself (incl. the URL this browser is using right now).
 */
export function useAdvertiseHints(enabled = true) {
  return useQuery<AdvertiseHints>({
    queryKey: ['advertise-hints'],
    queryFn: async () => {
      const { data } = await apiClient.get(
        `/cluster/advertise-hints?origin=${encodeURIComponent(window.location.origin)}`
      )
      return data.data as AdvertiseHints
    },
    enabled,
    staleTime: 60_000,
    retry: false,
  })
}
