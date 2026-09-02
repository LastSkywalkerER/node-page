import { z } from 'zod'

export const GatewayConfigSchema = z.object({
  enabled: z.boolean(),
  mode: z.string().default('managed'),
  node_mac: z.string().default(''),
  node_name: z.string().optional().default(''),
  node_system_id: z.string().optional().default(''),
  node_host_id: z.number().optional(),
  dynamic_dir: z.string().optional().default(''),
  http_port: z.number().optional().default(0),
  https_port: z.number().optional().default(0),
  acme_enabled: z.boolean().default(false),
  acme_email: z.string().optional().default(''),
  // Entrypoint read timeout (upload ceiling): 0 = default 24h, -1 = unlimited.
  request_read_timeout_seconds: z.number().optional().default(0),
  // Entrypoint hardening ('' = node-stats default: delete / strict).
  alias_headers_strategy: z.string().optional().default(''),
  encoded_path_policy: z.string().optional().default(''),
  // Managed Docker Traefik: extra EXISTING Docker networks it joins (targets by container name).
  docker_networks: z.array(z.string()).optional().default([]),
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
  target_https_port: z.number().optional().default(0),
  mode: z.string().optional().default('http'),
  target_host_mac: z.string().optional().default(''),
  target_label: z.string().optional().default(''),
  target_insecure_skip_verify: z.boolean().default(false),
  tls: z.boolean(),
  ip_allow_list: z.string().optional().default(''),
  max_conns_per_ip: z.number().optional().default(0),
  rate_limit_rps: z.number().optional().default(0),
  read_only: z.boolean().optional().default(false),
  upstream_timeout_seconds: z.number().optional().default(0),
  max_body_bytes: z.number().optional().default(0),
  // Hostname aliases, upstream shaping, extra access control, response hardening, redirect, stream.
  aliases: z.string().optional().default(''),
  strip_prefix: z.boolean().optional().default(false),
  add_prefix: z.string().optional().default(''),
  host_header_mode: z.string().optional().default(''),
  host_header_value: z.string().optional().default(''),
  target_server_name: z.string().optional().default(''),
  extra_targets: z.string().optional().default(''),
  health_check_path: z.string().optional().default(''),
  health_check_interval_seconds: z.number().optional().default(0),
  sticky: z.boolean().optional().default(false),
  retry_attempts: z.number().optional().default(0),
  request_headers: z.string().optional().default(''),
  response_headers: z.string().optional().default(''),
  forward_auth_url: z.string().optional().default(''),
  forward_auth_response_headers: z.string().optional().default(''),
  forward_auth_trust_forward_header: z.boolean().optional().default(false),
  security_headers: z.boolean().optional().default(false),
  hsts: z.boolean().optional().default(false),
  hsts_include_subdomains: z.boolean().optional().default(false),
  compress: z.boolean().optional().default(false),
  redirect_url: z.string().optional().default(''),
  redirect_permanent: z.boolean().optional().default(false),
  redirect_preserve_path: z.boolean().optional().default(false),
  protocol: z.string().optional().default(''),
  listen_port: z.number().optional().default(0),
  enabled: z.boolean(),
  basic_auth_users: z.array(z.string()).default([]),
  public_url: z.string(),
  protected: z.boolean(),
  target_url: z.string().optional().default(''),
  effective_url: z.string().optional().default(''),
  rewritten: z.boolean().optional().default(false),
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
  blocks_file_path: z.string().optional().default(''),
  route_count: z.number().default(0),
  last_render_at: z.string().nullable().optional(),
  last_error: z.string().optional().default(''),
  controller: ControllerStatusSchema.nullable().optional(),
  traefik_healthy: z.boolean().nullable().optional(),
  traefik_detail: z.string().optional().default(''),
})
export type GatewayStatus = z.infer<typeof GatewayStatusSchema>

export const GatewayCapabilitiesSchema = z.object({
  can_manage: z.boolean(),
  manage_kind: z.string().optional().default(''),
  manage_reason: z.string().optional().default(''),
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

export type RouteMode = 'http' | 'passthrough' | 'redirect' | 'stream'

/** Zero values for every feature field — spread into a new request / a passthrough reset. */
export const EMPTY_ROUTE_FEATURES = {
  aliases: '',
  strip_prefix: false,
  add_prefix: '',
  host_header_mode: '' as const,
  host_header_value: '',
  target_server_name: '',
  extra_targets: '',
  health_check_path: '',
  health_check_interval_seconds: 0,
  sticky: false,
  retry_attempts: 0,
  request_headers: '',
  response_headers: '',
  forward_auth_url: '',
  forward_auth_response_headers: '',
  forward_auth_trust_forward_header: false,
  security_headers: false,
  hsts: false,
  hsts_include_subdomains: false,
  compress: false,
  redirect_url: '',
  redirect_permanent: false,
  redirect_preserve_path: false,
  protocol: '' as const,
  listen_port: 0,
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
  mode: RouteMode
  target_https_port: number
  basic_auth: BasicAuthInput[]
  ip_allow_list: string
  /** Extra hostnames (comma-separated) served by the same route. */
  aliases: string
  /** Upstream shaping (http mode). */
  strip_prefix: boolean
  add_prefix: string
  host_header_mode: '' | 'client' | 'upstream' | 'custom'
  host_header_value: string
  target_server_name: string
  extra_targets: string
  health_check_path: string
  health_check_interval_seconds: number
  sticky: boolean
  retry_attempts: number
  request_headers: string
  response_headers: string
  /** Forward auth (SSO). */
  forward_auth_url: string
  forward_auth_response_headers: string
  forward_auth_trust_forward_header: boolean
  /** Response hardening / compression. */
  security_headers: boolean
  hsts: boolean
  hsts_include_subdomains: boolean
  compress: boolean
  /** Redirect mode. */
  redirect_url: string
  redirect_permanent: boolean
  redirect_preserve_path: boolean
  /** Stream mode. */
  protocol: 'tcp' | 'udp' | ''
  listen_port: number
  /** Per-route request limits (http mode only; 0/false = off). */
  max_conns_per_ip: number
  rate_limit_rps: number
  read_only: boolean
  upstream_timeout_seconds: number
  max_body_bytes: number
  enabled?: boolean
}

/** Defaults for a NEW http route — a bounded-but-generous safety net the admin can loosen per route. */
export const DEFAULT_ROUTE_LIMITS = {
  max_conns_per_ip: 100,
  rate_limit_rps: 0,
  read_only: false,
  upstream_timeout_seconds: 60,
  max_body_bytes: 0,
} as const

export const ConnEventSchema = z.object({
  ts: z.number(),
  ip: z.string(),
  host: z.string().optional().default(''),
  method: z.string().optional().default(''),
  path: z.string().optional().default(''),
  status: z.number().default(0),
  route_id: z.string().optional().default(''),
  blocked: z.boolean().optional().default(false),
  dur_ms: z.number().optional().default(0),
  ua: z.string().optional().default(''),
  tls: z.boolean().optional().default(false),
})
export type ConnEvent = z.infer<typeof ConnEventSchema>

export const ConnIPSchema = z.object({
  ip: z.string(),
  count: z.number(),
  s2xx: z.number().default(0),
  s4xx: z.number().default(0),
  s5xx: z.number().default(0),
  no_route: z.number().default(0),
  scanner_hits: z.number().default(0),
  blocked: z.number().default(0),
  first_seen: z.number().default(0),
  last_seen: z.number().default(0),
  last_path: z.string().optional().default(''),
  last_host: z.string().optional().default(''),
  last_ua: z.string().optional().default(''),
  hosts: z.number().default(0),
  suspicion: z.number().default(0),
  is_blocked: z.boolean().optional().default(false),
})
export type ConnIP = z.infer<typeof ConnIPSchema>

export const RatePointSchema = z.object({
  ts: z.number(),
  total: z.number().default(0),
  e4xx: z.number().default(0),
  e5xx: z.number().default(0),
  blocked: z.number().default(0),
})

export const ConnectionsViewSchema = z.object({
  available: z.boolean(),
  reason: z.string().optional().default(''),
  since_ts: z.number().optional().default(0),
  total: z.number().default(0),
  no_route: z.number().default(0),
  blocked_total: z.number().default(0),
  unique_ips: z.number().default(0),
  minutes: z.array(RatePointSchema).default([]),
  hours: z.array(RatePointSchema).default([]),
  top: z.array(ConnIPSchema).default([]),
  recent: z.array(ConnEventSchema).default([]),
})
export type ConnectionsView = z.infer<typeof ConnectionsViewSchema>

export const GatewayBlockSchema = z.object({
  id: z.number().optional().default(0),
  block_id: z.string(),
  cidr: z.string(),
  reason: z.string().optional().default(''),
  source: z.string().optional().default('manual'),
  created_by: z.string().optional().default(''),
  expires_at: z.string().nullable().optional(),
  created_at: z.string().optional().default(''),
})
export type GatewayBlock = z.infer<typeof GatewayBlockSchema>

export interface BlockRequest {
  cidr: string
  reason: string
  ttl_hours: number
  force?: boolean
}

/** One Traefik config file node-stats writes on the gateway node (GET /gateway/files). */
export const GatewayConfigFileSchema = z.object({
  name: z.string(),
  kind: z.string(),
  path: z.string().optional().default(''),
  content: z.string().optional().default(''),
  size: z.number().optional().default(0),
  modified: z.string().nullable().optional(),
  missing: z.boolean().optional().default(false),
  note: z.string().optional().default(''),
})
export type GatewayConfigFile = z.infer<typeof GatewayConfigFileSchema>
