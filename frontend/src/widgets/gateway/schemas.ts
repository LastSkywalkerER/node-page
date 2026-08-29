import { z } from 'zod'

export const GatewayConfigSchema = z.object({
  enabled: z.boolean(),
  mode: z.string().default('managed'),
  node_mac: z.string().default(''),
  node_name: z.string().optional().default(''),
  dynamic_dir: z.string().optional().default(''),
  http_port: z.number().optional().default(0),
  https_port: z.number().optional().default(0),
  acme_enabled: z.boolean().default(false),
  acme_email: z.string().optional().default(''),
  acme_staging: z.boolean().optional().default(false),
})
export type GatewayConfig = z.infer<typeof GatewayConfigSchema>

export const GatewayRouteSchema = z.object({
  id: z.number(),
  route_id: z.string(),
  name: z.string(),
  domain: z.string(),
  path_prefix: z.string().optional().default(''),
  target_scheme: z.string(),
  target_host: z.string(),
  target_port: z.number(),
  target_host_mac: z.string().optional().default(''),
  target_label: z.string().optional().default(''),
  target_insecure_skip_verify: z.boolean().default(false),
  tls: z.boolean(),
  ip_allow_list: z.string().optional().default(''),
  enabled: z.boolean(),
  basic_auth_users: z.array(z.string()).default([]),
  public_url: z.string(),
  protected: z.boolean(),
  created_at: z.string(),
  updated_at: z.string(),
})
export type GatewayRoute = z.infer<typeof GatewayRouteSchema>

export const ControllerStatusSchema = z.object({
  generation: z.number().optional(),
  phase: z.string().optional().default(''),
  message: z.string().optional().default(''),
  error: z.string().optional().default(''),
  updated_at: z.string().optional().default(''),
})

export const GatewayStatusSchema = z.object({
  is_gateway_node: z.boolean(),
  mode: z.string().optional().default(''),
  file_path: z.string().optional().default(''),
  route_count: z.number().default(0),
  last_render_at: z.string().nullable().optional(),
  last_error: z.string().optional().default(''),
  controller: ControllerStatusSchema.nullable().optional(),
  traefik_healthy: z.boolean().nullable().optional(),
})
export type GatewayStatus = z.infer<typeof GatewayStatusSchema>

export const GatewayCapabilitiesSchema = z.object({
  can_manage: z.boolean(),
  running_in_docker: z.boolean(),
  managed_externally: z.boolean(),
  local_host_id: z.number(),
  local_mac: z.string().default(''),
  managed_dynamic_dir: z.string().default(''),
})
export type GatewayCapabilities = z.infer<typeof GatewayCapabilitiesSchema>

export const GatewayStateSchema = z.object({
  config: GatewayConfigSchema,
  routes: z.array(GatewayRouteSchema),
  status: GatewayStatusSchema,
  capabilities: GatewayCapabilitiesSchema,
})
export type GatewayState = z.infer<typeof GatewayStateSchema>

export const GatewayTargetSchema = z.object({
  host_id: z.number(),
  host_name: z.string(),
  host_mac: z.string().default(''),
  ipv4: z.string(),
  app: z.string(),
  container: z.string().optional().default(''),
  port: z.number(),
  private_port: z.number().optional().default(0),
  image: z.string().optional().default(''),
})
export type GatewayTarget = z.infer<typeof GatewayTargetSchema>

export interface BasicAuthInput {
  user: string
  password: string
}

export interface RouteRequest {
  name: string
  domain: string
  path_prefix: string
  target_scheme: string
  target_host: string
  target_port: number
  target_host_mac: string
  target_label: string
  target_insecure_skip_verify: boolean
  tls: boolean
  basic_auth: BasicAuthInput[]
  ip_allow_list: string
  enabled?: boolean
}
