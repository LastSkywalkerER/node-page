import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { apiClient } from '@/shared/lib/api'
import {
  ConnectionsViewSchema,
  GatewayBlockSchema,
  GatewayConfigFileSchema,
  GatewayStateSchema,
  GatewayTargetSchema,
  GatewayConfigSchema,
  GatewayRouteSchema,
  type GatewayState,
  type GatewayTarget,
  type GatewayConfig,
  type GatewayRoute,
  type RouteRequest,
  type ConnectionsView,
  type GatewayBlock,
  type GatewayConfigFile,
  type BlockRequest,
  DockerNetworkSchema,
  type DockerNetwork,
  ImportResultSchema,
  type ImportResult,
} from './schemas'
import { z } from 'zod'

function apiError(e: unknown): Error {
  if (isAxiosError(e)) {
    const data = e.response?.data as { error?: string } | undefined
    if (data?.error) return new Error(data.error)
  }
  return e instanceof Error ? e : new Error(String(e))
}

const KEY = ['gateway'] as const

/** Gateway config + routes + this node's status (admin-only endpoint). */
export function useGateway(enabled = true) {
  return useQuery<GatewayState>({
    queryKey: KEY,
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway')
      return GatewayStateSchema.parse(data)
    },
    enabled,
    refetchInterval: 5000,
    staleTime: 2000,
  })
}

export function useGatewayTargets(enabled = true) {
  return useQuery<GatewayTarget[]>({
    queryKey: [...KEY, 'targets'],
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway/targets')
      return z.array(GatewayTargetSchema).parse(data.targets ?? [])
    },
    enabled,
    staleTime: 15000,
  })
}

/** Docker networks on THIS node the managed Traefik could join (config card picker). */
export function useDockerNetworks(enabled = true) {
  return useQuery<{ networks: DockerNetwork[]; error?: string }>({
    queryKey: [...KEY, 'docker-networks'],
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway/docker-networks')
      return { networks: z.array(DockerNetworkSchema).parse(data.networks ?? []), error: data.error }
    },
    enabled,
    staleTime: 15000,
  })
}

export function useSetGatewayConfig() {
  const qc = useQueryClient()
  return useMutation<GatewayConfig, Error, GatewayConfig>({
    mutationFn: async (cfg) => {
      try {
        const { data } = await apiClient.put('/gateway/config', cfg)
        return GatewayConfigSchema.parse(data.config)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  })
}

/** Import routes from a pasted Traefik dynamic config (or native routes YAML); dry_run previews. */
export function useImportRoutes() {
  const qc = useQueryClient()
  return useMutation<ImportResult, Error, { yaml: string; dry_run: boolean }>({
    mutationFn: async (body) => {
      try {
        const { data } = await apiClient.post('/gateway/import', body)
        return ImportResultSchema.parse(data)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: (_r, vars) => {
      if (!vars.dry_run) void qc.invalidateQueries({ queryKey: KEY })
    },
  })
}

export function useCreateRoute() {
  const qc = useQueryClient()
  return useMutation<GatewayRoute, Error, RouteRequest>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/gateway/routes', req)
        return GatewayRouteSchema.parse(data.route)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  })
}

export function useUpdateRoute() {
  const qc = useQueryClient()
  return useMutation<GatewayRoute, Error, { routeId: string; req: RouteRequest }>({
    mutationFn: async ({ routeId, req }) => {
      try {
        const { data } = await apiClient.put(`/gateway/routes/${routeId}`, req)
        return GatewayRouteSchema.parse(data.route)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  })
}

export function useDeleteRoute() {
  const qc = useQueryClient()
  return useMutation<void, Error, string>({
    mutationFn: async (routeId) => {
      try {
        await apiClient.delete(`/gateway/routes/${routeId}`)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  })
}

/** Managed Traefik logs — only meaningful on the gateway node. */
export function useGatewayLogs(enabled: boolean, tail = 300, live = true) {
  return useQuery<{ logs: string; error?: string }>({
    queryKey: [...KEY, 'logs', tail],
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway/logs', { params: { tail } })
      return data as { logs: string; error?: string }
    },
    enabled,
    refetchInterval: live ? 5000 : false,
  })
}

/** Traefik config files node-stats owns on this node — gateway node only. */
export function useGatewayFiles(enabled: boolean) {
  return useQuery<{ files: GatewayConfigFile[]; error?: string }>({
    queryKey: [...KEY, 'files'],
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway/files')
      return { files: z.array(GatewayConfigFileSchema).parse(data.files ?? []), error: data.error as string | undefined }
    },
    enabled,
    refetchInterval: 10000,
  })
}

export interface PublicProbe { node: string; location?: string; ok: boolean; time_ms?: number; error?: string }
export interface PublicPortCheck { port: number; reachable: boolean; probes: PublicProbe[] }
export interface PublicCheckResult { public_ip: string; detected: boolean; candidates?: Record<string, string>; ports: PublicPortCheck[]; provider: string; error?: string }

/** Ask an external service (check-host.net) whether the gateway ports are open from the internet. */
export function useCheckPublic() {
  return useMutation<PublicCheckResult, Error, { target?: string }>({
    mutationFn: async (body) => {
      try {
        const { data } = await apiClient.post('/gateway/check-public', body ?? {}, { timeout: 60000 })
        return data as PublicCheckResult
      } catch (e) {
        throw apiError(e)
      }
    },
  })
}

export interface TargetCheck {
  checked: string
  reachable: boolean
  error?: string
  /** upstream completed a TLS handshake → route must use https */
  tls: boolean
  cert_subject?: string
  cert_trusted: boolean
}

export function useCheckTarget() {
  return useMutation<TargetCheck, Error, { host: string; host_mac?: string; port: number }>({
    mutationFn: async (body) => {
      try {
        const { data } = await apiClient.post('/gateway/check', body)
        return data as TargetCheck
      } catch (e) {
        throw apiError(e)
      }
    },
  })
}

/** Turn a stored route back into an editable request (passwords stay blank = keep). */
export function routeToRequest(r: GatewayRoute): RouteRequest {
  return {
    name: r.name,
    domain: r.domain,
    path_prefix: r.path_prefix,
    target_scheme: r.target_scheme,
    target_host: r.target_host,
    target_port: r.target_port,
    target_host_mac: r.target_host_mac,
    target_label: r.target_label,
    target_insecure_skip_verify: r.target_insecure_skip_verify,
    tls: r.tls,
    mode: (['passthrough', 'redirect', 'stream'] as const).find((m) => m === r.mode) ?? 'http',
    target_https_port: r.target_https_port || 443,
    basic_auth: r.basic_auth_users.map((u) => ({ user: u, password: '' })),
    ip_allow_list: r.ip_allow_list,
    max_conns_per_ip: r.max_conns_per_ip,
    rate_limit_rps: r.rate_limit_rps,
    read_only: r.read_only,
    upstream_timeout_seconds: r.upstream_timeout_seconds,
    max_body_bytes: r.max_body_bytes,
    aliases: r.aliases,
    strip_prefix: r.strip_prefix,
    add_prefix: r.add_prefix,
    host_header_mode: (['client', 'upstream', 'custom'] as const).find((m) => m === r.host_header_mode) ?? '',
    host_header_value: r.host_header_value,
    target_server_name: r.target_server_name,
    extra_targets: r.extra_targets,
    health_check_path: r.health_check_path,
    health_check_interval_seconds: r.health_check_interval_seconds,
    sticky: r.sticky,
    retry_attempts: r.retry_attempts,
    request_headers: r.request_headers,
    response_headers: r.response_headers,
    forward_auth_url: r.forward_auth_url,
    forward_auth_response_headers: r.forward_auth_response_headers,
    forward_auth_trust_forward_header: r.forward_auth_trust_forward_header,
    security_headers: r.security_headers,
    hsts: r.hsts,
    hsts_include_subdomains: r.hsts_include_subdomains,
    compress: r.compress,
    redirect_url: r.redirect_url,
    redirect_permanent: r.redirect_permanent,
    redirect_preserve_path: r.redirect_preserve_path,
    protocol: r.protocol === 'udp' ? 'udp' : r.protocol === 'tcp' ? 'tcp' : '',
    listen_port: r.listen_port,
    enabled: r.enabled,
  }
}

/** Live connection stats — served by (or proxied to) the gateway node. */
export function useGatewayConnections(enabled: boolean) {
  return useQuery<ConnectionsView>({
    queryKey: [...KEY, 'connections'],
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway/connections')
      return ConnectionsViewSchema.parse(data)
    },
    enabled,
    refetchInterval: 5000,
    staleTime: 2000,
  })
}

/** Blocked clients (replicated deny list — served by any node). */
export function useGatewayBlocks(enabled: boolean) {
  return useQuery<GatewayBlock[]>({
    queryKey: [...KEY, 'blocks'],
    queryFn: async () => {
      const { data } = await apiClient.get('/gateway/blocks')
      return z.array(GatewayBlockSchema).parse(data.blocks ?? [])
    },
    enabled,
    refetchInterval: 10000,
  })
}

export function useCreateBlock() {
  const qc = useQueryClient()
  return useMutation<GatewayBlock, Error, BlockRequest>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/gateway/blocks', req)
        return GatewayBlockSchema.parse(data.block)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: [...KEY, 'blocks'] })
      qc.invalidateQueries({ queryKey: [...KEY, 'connections'] })
    },
  })
}

export function useDeleteBlock() {
  const qc = useQueryClient()
  return useMutation<void, Error, string>({
    mutationFn: async (blockId) => {
      try {
        await apiClient.delete(`/gateway/blocks/${blockId}`)
      } catch (e) {
        throw apiError(e)
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: [...KEY, 'blocks'] })
      qc.invalidateQueries({ queryKey: [...KEY, 'connections'] })
    },
  })
}
