import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { apiClient } from '@/shared/lib/api'

export interface SettingsConfig {
  addr: string
  gin_mode: string
  debug: string
  node_stats_hostname: string
  node_stats_ipv4: string
  raft_enabled: string
  raft_bind_addr: string
  raft_advertise_addr: string
  db_type: string
  prometheus_enabled: string
  prometheus_auth: string
  prometheus_token_set: boolean
  managed_externally: boolean
}

export interface RestartResult {
  restart_pending: boolean
  restart_required: boolean
  error?: string
}

export function useSettingsConfig() {
  return useQuery<SettingsConfig>({
    queryKey: ['settings', 'config'],
    queryFn: async () => {
      const res = await apiClient.get<{ data: SettingsConfig }>('/settings/config')
      return res.data.data
    },
  })
}

// notifyRestart surfaces what happens after a settings change that needs the
// process to restart: a controller-driven recreate (Docker) vs. a manual
// restart hint (native / externally managed).
export function notifyRestart(r: RestartResult) {
  if (r.restart_pending) {
    toast.success('Saved — the app is recreating to apply. This can take a moment.')
  } else {
    toast.success('Saved. Restart the app (or redeploy) to apply the change.')
  }
}

export function useSavePrometheus() {
  const qc = useQueryClient()
  return useMutation<RestartResult, Error, { enabled: boolean; auth: boolean; token?: string }>({
    mutationFn: async (body) => {
      const res = await apiClient.post<{ data: RestartResult }>('/settings/prometheus', body)
      return res.data.data
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['settings', 'config'] })
      notifyRestart(r)
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to save Prometheus settings')
    },
  })
}

export function useSavePorts() {
  const qc = useQueryClient()
  return useMutation<RestartResult, Error, { http_port: string; raft_port?: string }>({
    mutationFn: async (body) => {
      const res = await apiClient.post<{ data: RestartResult }>('/settings/ports', body)
      return res.data.data
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['settings', 'config'] })
      notifyRestart(r)
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to change ports')
    },
  })
}

export interface DbSwitchBody {
  db_type: 'sqlite' | 'postgres'
  db_managed: boolean
  db_host?: string
  db_port?: string
  db_name?: string
  db_user?: string
  db_password?: string
  db_sslmode?: string
  db_sqlite_path?: string
  acknowledge_data_loss: boolean
}

export function useTestDb() {
  return useMutation<{ ok: boolean; error?: string }, Error, Record<string, string>>({
    mutationFn: async (body) => {
      const res = await apiClient.post<{ data: { ok: boolean; error?: string } }>('/settings/db/test', body)
      return res.data.data
    },
  })
}

export function useSwitchDb() {
  return useMutation<RestartResult & { db_mode: string }, Error, DbSwitchBody>({
    mutationFn: async (body) => {
      const res = await apiClient.post<{ data: RestartResult & { db_mode: string } }>('/settings/db/switch', body)
      return res.data.data
    },
    onSuccess: (r) => notifyRestart(r),
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to switch database')
    },
  })
}

export interface ServerSettingsBody {
  gin_mode?: string
  debug?: boolean
  hostname?: string
  ipv4?: string
  addr?: string
}

export function useSaveServer() {
  const qc = useQueryClient()
  return useMutation<RestartResult, Error, ServerSettingsBody>({
    mutationFn: async (body) => {
      const res = await apiClient.post<{ data: RestartResult }>('/settings/server', body)
      return res.data.data
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['settings', 'config'] })
      notifyRestart(r)
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to save server settings')
    },
  })
}
