import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { apiClient } from '@/shared/lib/api'
import {
  GatewayStateSchema,
  GatewayTargetSchema,
  GatewayConfigSchema,
  GatewayRouteSchema,
  type GatewayState,
  type GatewayTarget,
  type GatewayConfig,
  type GatewayRoute,
  type RouteRequest,
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

export function useCheckTarget() {
  return useMutation<{ reachable: boolean; error?: string }, Error, { host: string; port: number }>({
    mutationFn: async (body) => {
      try {
        const { data } = await apiClient.post('/gateway/check', body)
        return data as { reachable: boolean; error?: string }
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
    basic_auth: r.basic_auth_users.map((u) => ({ user: u, password: '' })),
    ip_allow_list: r.ip_allow_list,
    enabled: r.enabled,
  }
}
