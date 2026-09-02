import { useEffect, useMemo, useRef, useState } from 'react'
import { format } from 'date-fns'
import { toast } from 'sonner'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { Globe, Plus, Trash2, Pencil, ExternalLink, ShieldAlert, ShieldCheck, Lock, Activity, X, ScrollText, RefreshCw, Radar, Gauge, FileCode2, Copy, Check, Pause, Play } from 'lucide-react'
import { LogViewer } from '@/widgets/applications/LogViewer'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/shared/ui/switch'
import { cn } from '@/lib/utils'
import { useHosts } from '@/widgets/hosts/useHosts'
import {
  useGateway,
  useGatewayTargets,
  useSetGatewayConfig,
  useCreateRoute,
  useUpdateRoute,
  useDeleteRoute,
  useCheckTarget,
  useGatewayLogs,
  useGatewayFiles,
  useCheckPublic,
  routeToRequest,
  type PublicCheckResult,
  type TargetCheck,
} from './useGateway'
import { DEFAULT_ROUTE_LIMITS, EMPTY_ROUTE_FEATURES } from './schemas'
import type { GatewayConfig, GatewayRoute, GatewayState, GatewayTarget, RouteRequest, BasicAuthInput, GatewayConfigFile } from './schemas'
import { ConnectionsCard, BlocksCard } from './ConnectionsCard'

const selectCls =
  'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50'
const textareaCls =
  'flex min-h-16 w-full rounded-md border border-input bg-background px-3 py-1.5 font-mono text-xs shadow-xs outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50'

/** First of a comma-separated prefix list ('' when none / '/'). */
function firstPrefix(csv: string): string {
  const p = csv
    .split(',')
    .map((s) => s.trim())
    .find((s) => s && s !== '/')
  return p ?? ''
}

function fmtTime(iso?: string | null): string {
  if (!iso) return 'never'
  const d = new Date(iso)
  return isNaN(d.getTime()) ? 'never' : format(d, 'HH:mm:ss dd.MM.yyyy')
}

// ---------------------------------------------------------------------------
// Gateway node / mode configuration
// ---------------------------------------------------------------------------

function ConfigCard({ state }: { state: GatewayState }) {
  const { data: hostsData } = useHosts()
  const save = useSetGatewayConfig()
  const [cfg, setCfg] = useState<GatewayConfig>(state.config)
  const [dirty, setDirty] = useState(false)

  // Follow server state until the admin starts editing.
  useEffect(() => {
    if (!dirty) setCfg(state.config)
  }, [state.config, dirty])

  const update = (patch: Partial<GatewayConfig>) => {
    setCfg((c) => ({ ...c, ...patch }))
    setDirty(true)
  }

  // Only agent-backed machines can host the gateway (connector-only rows have
  // no node-stats process to render the config).
  const candidates = useMemo(
    () => (hostsData?.hosts ?? []).filter((h) => h.mac_address && h.source !== 'connector'),
    [hostsData]
  )
  const [publicResult, setPublicResult] = useState<PublicCheckResult | null>(null)
  const caps = state.capabilities
  // Identity is by stable machine id first, MAC second: Docker bridge
  // containers share 02:42:… MACs across machines, so MAC alone can point the
  // select (and "this node") at the wrong host.
  const localHost = (hostsData?.hosts ?? []).find((h) => h.id === caps.local_host_id)
  const isLocalHost = (h: { id: number; mac_address: string }) =>
    localHost ? h.id === localHost.id : h.mac_address.toLowerCase() === caps.local_mac.toLowerCase()
  const selectedHost = useMemo(() => {
    if (cfg.node_host_id) return candidates.find((h) => h.id === cfg.node_host_id)
    if (cfg.node_system_id) {
      const byId = candidates.find((h) => h.system_host_id === cfg.node_system_id || h.hardware_uuid === cfg.node_system_id)
      if (byId) return byId
    }
    return cfg.node_mac ? candidates.find((h) => h.mac_address.toLowerCase() === cfg.node_mac.toLowerCase()) : undefined
  }, [candidates, cfg.node_host_id, cfg.node_system_id, cfg.node_mac])
  // Pre-save: trust the selection; otherwise trust the backend's verdict.
  const selectedIsLocal = !!selectedHost && isLocalHost(selectedHost) && (dirty || state.status.is_gateway_node)
  const pickNode = (id: string) => {
    const h = candidates.find((x) => String(x.id) === id)
    if (!h) {
      update({ node_mac: '', node_host_id: undefined, node_system_id: '', node_name: '' })
      return
    }
    update({ node_mac: h.mac_address, node_host_id: h.id, node_system_id: h.system_host_id || h.hardware_uuid || '', node_name: h.name })
  }

  const onSave = () => {
    save.mutate(cfg, {
      onSuccess: () => {
        setDirty(false)
        toast.success('Gateway configuration saved')
      },
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Globe className="h-4 w-4" /> Gateway node
            </CardTitle>
            <CardDescription>
              One node of the cluster terminates public traffic and forwards it to services on any machine. Routes
              below are rendered as Traefik file-provider config on that node.
            </CardDescription>
          </div>
          <Switch checked={cfg.enabled} onCheckedChange={(v) => update({ enabled: v })} />
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-1.5">
            <Label>Gateway node</Label>
            <select className={selectCls} value={selectedHost ? String(selectedHost.id) : ''} onChange={(e) => pickNode(e.target.value)}>
              <option value="">— pick a node —</option>
              {candidates.map((h) => (
                <option key={h.id} value={String(h.id)}>
                  {h.display_name || h.name}
                  {h.ipv4 ? ` (${h.ipv4})` : ''}
                  {isLocalHost(h) ? ' · this node' : ''}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label>Mode</Label>
            <select className={selectCls} value={cfg.mode} onChange={(e) => update({ mode: e.target.value })}>
              <option value="managed">Managed — node-stats runs Traefik (Docker container or systemd service)</option>
              <option value="external">External — write into an existing Traefik's dynamic dir</option>
            </select>
          </div>
        </div>

        {cfg.mode === 'managed' ? (
          <>
            <div className="grid gap-4 md:grid-cols-2">
              <div className="space-y-1.5">
                <Label>HTTP port</Label>
                <Input
                  type="number"
                  value={cfg.http_port || 80}
                  onChange={(e) => update({ http_port: Number(e.target.value) })}
                />
              </div>
              <div className="space-y-1.5">
                <Label>HTTPS port</Label>
                <Input
                  type="number"
                  value={cfg.https_port || 443}
                  onChange={(e) => update({ https_port: Number(e.target.value) })}
                />
              </div>
            </div>
            {state.capabilities.running_in_docker && (
              <div className="space-y-1.5">
                <Label>Extra Docker networks (optional, comma-separated)</Label>
                <Input
                  placeholder="nginxproxymanager, myapp_default"
                  value={(cfg.docker_networks ?? []).join(', ')}
                  onChange={(e) =>
                    update({
                      docker_networks: e.target.value
                        .split(',')
                        .map((s) => s.trim())
                        .filter(Boolean),
                    })
                  }
                />
                <p className="text-xs text-muted-foreground">
                  Existing networks of other stacks the managed Traefik container also joins, so their containers can be
                  targeted <b>by container name</b> (<span className="font-mono">http://grafana:3000</span>) — including
                  ports published on <span className="font-mono">127.0.0.1</span> only, which no other bridge network can
                  reach. Find them with <span className="font-mono">docker network ls</span>. Changing this restarts Traefik.
                </p>
              </div>
            )}
            <div className="space-y-1.5">
              <Label>Request read timeout (upload ceiling)</Label>
              <select
                className={selectCls}
                value={String(cfg.request_read_timeout_seconds || 0)}
                onChange={(e) => update({ request_read_timeout_seconds: Number(e.target.value) })}
              >
                {!READ_TIMEOUT_PRESETS.some((p) => p.value === (cfg.request_read_timeout_seconds || 0)) && (
                  <option value={String(cfg.request_read_timeout_seconds)}>{cfg.request_read_timeout_seconds} seconds (custom)</option>
                )}
                {READ_TIMEOUT_PRESETS.map((p) => (
                  <option key={p.value} value={String(p.value)}>
                    {p.label}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                How long a client may take to send one request, body included — in practice the largest/slowest upload a
                file service (MinIO, Nextcloud, Immich…) behind the gateway can accept. Traefik applies it on the
                listener, so it is gateway-wide; tighten individual routes with the <b>Request limits</b> in the route
                form. Changing it restarts the managed Traefik.
              </p>
            </div>
            <div className="rounded-lg border border-border/60 p-3 space-y-3">
              <div>
                <div className="text-sm font-medium">Request hardening</div>
                <p className="text-xs text-muted-foreground">
                  Entry-point filters applied before any route (Traefik ≥ 3.7.12 — the managed Traefik is pinned to
                  that line, an older native binary is upgraded automatically). Changing them restarts Traefik.
                </p>
              </div>
              <div className="grid gap-4 md:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>Header-alias spoofing</Label>
                  <select
                    className={selectCls}
                    value={cfg.alias_headers_strategy || 'delete'}
                    onChange={(e) => update({ alias_headers_strategy: e.target.value })}
                  >
                    <option value="delete">Delete aliased headers (default)</option>
                    <option value="reject">Reject the request with 400</option>
                    <option value="keep">Keep — forward as-is (Traefik default)</option>
                  </select>
                  <p className="text-xs text-muted-foreground">
                    Headers like <code>X_Forwarded_For</code> or <code>X.Real.IP</code> collapse into the real{' '}
                    <code>X-Forwarded-For</code> / <code>X-Real-IP</code> inside PHP, CGI, WSGI or nginx backends, letting
                    a client spoof the values Traefik sets. Delete is safe for practically every app.
                  </p>
                </div>
                <div className="space-y-1.5">
                  <Label>Percent-encoded path characters</Label>
                  <select
                    className={selectCls}
                    value={cfg.encoded_path_policy || 'strict'}
                    onChange={(e) => update({ encoded_path_policy: e.target.value })}
                  >
                    <option value="strict">Strict — reject %2F %5C %00, allow the rest (default)</option>
                    <option value="permissive">Permissive — allow all (Traefik default)</option>
                    <option value="paranoid">Paranoid — reject all encoded specials</option>
                  </select>
                  <p className="text-xs text-muted-foreground">
                    Encoded slashes, backslashes and NULs let a sloppy backend see a different path than the router
                    matched (split-view). Traefik still prints one informational warning at start-up whenever any
                    character is rejected — that is this choice describing itself, not an error. Switch to permissive
                    only if an app legitimately needs <code>%2F</code> in paths (some S3 clients, Gitea/GitLab API).
                  </p>
                </div>
              </div>
            </div>
            <div className="rounded-lg border border-border/60 p-3 space-y-3">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <div className="text-sm font-medium">Let's Encrypt certificates</div>
                  <p className="text-xs text-muted-foreground">
                    HTTP-01 challenge — port 80 of the gateway node must be reachable from the internet and every
                    route's domain must resolve to it.
                  </p>
                </div>
                <Switch checked={cfg.acme_enabled} onCheckedChange={(v) => update({ acme_enabled: v })} />
              </div>
              {cfg.acme_enabled && (
                <div className="grid gap-4 md:grid-cols-2">
                  <div className="space-y-1.5">
                    <Label>Contact e-mail</Label>
                    <Input
                      type="email"
                      placeholder="ops@example.com"
                      value={cfg.acme_email}
                      onChange={(e) => update({ acme_email: e.target.value })}
                    />
                  </div>
                  <div className="flex items-end pb-1 text-xs text-muted-foreground">
                    {publicResult == null ? (
                      <span>
                        Before enabling, run <b>Check from the internet</b> below — Let's Encrypt has strict limits on
                        failed attempts (5/hour per domain), so make sure :80 is open first.
                      </span>
                    ) : publicResult.ports.find((p) => p.port === (cfg.http_port || 80))?.reachable ? (
                      <span className="text-emerald-500">:{cfg.http_port || 80} is open from the internet — HTTP-01 can succeed.</span>
                    ) : (
                      <span className="text-red-400">
                        :{cfg.http_port || 80} is NOT reachable from the internet — Let's Encrypt will fail and burn its
                        rate limit. Open / forward the port first.
                      </span>
                    )}
                  </div>
                </div>
              )}
            </div>
            {selectedIsLocal && caps.can_manage && (
              <p className="text-xs text-muted-foreground">
                On this node Traefik runs as{' '}
                {caps.manage_kind === 'systemd'
                  ? 'a systemd service (node-stats downloads the binary and manages the unit)'
                  : 'a compose service started by the controller sidecar'}
                .
              </p>
            )}
            {selectedIsLocal && !caps.can_manage && (
              <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs">
                <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                <span>
                  This node can't run a managed Traefik: {caps.manage_reason || 'no supported backend.'} Use{' '}
                  <b>External</b> mode and point at your reverse proxy's dynamic-config directory instead.
                </span>
              </div>
            )}
          </>
        ) : (
          <div className="space-y-1.5">
            <Label>Traefik dynamic-config directory (as seen by node-stats on the gateway node)</Label>
            <Input
              placeholder="/etc/traefik/dynamic"
              value={cfg.dynamic_dir}
              onChange={(e) => update({ dynamic_dir: e.target.value })}
            />
            <p className="text-xs text-muted-foreground">
              Must be bind-mounted <b>read-write</b> into the node-stats container (installer-owned{' '}
              <code>docker-compose.override.yml</code>) and match Traefik's <code>providers.file.directory</code>.
              node-stats writes exactly one file there — <code>node-stats.yml</code> — and never touches anything
              else. Routes use the <code>web</code>/<code>websecure</code> entrypoints and the <code>le</code> cert
              resolver; adjust your static config to match or leave TLS off.
            </p>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-3">
          <StatusLine state={state} />
          <Button size="sm" onClick={onSave} disabled={save.isPending || !dirty}>
            {save.isPending ? 'Saving…' : 'Save'}
          </Button>
        </div>
        {state.config.enabled && state.status.is_gateway_node && <PublicCheck onResult={setPublicResult} />}
      </CardContent>
    </Card>
  )
}

/**
 * "Can the internet reach my gateway?" — asks check-host.net to TCP-connect to
 * this node's public IP on the HTTP/HTTPS ports from a few locations. Explicit
 * button (it hands the public IP + ports to a third party).
 */
const PUBLIC_TARGET_KEY = 'gateway.publicCheckTarget'

function PublicCheck({ onResult }: { onResult?: (r: PublicCheckResult) => void }) {
  const check = useCheckPublic()
  const r = check.data
  const [target, setTarget] = useState<string>(() => {
    try {
      return localStorage.getItem(PUBLIC_TARGET_KEY) ?? ''
    } catch {
      return ''
    }
  })
  const run = () => {
    try {
      localStorage.setItem(PUBLIC_TARGET_KEY, target.trim())
    } catch {
      /* ignore */
    }
    check.mutate(
      { target: target.trim() },
      { onSuccess: (r) => onResult?.(r), onError: (e) => toast.error(e.message) }
    )
  }
  return (
    <div className="rounded-lg border border-border/60 p-3 text-xs">
      <div className="text-muted-foreground">
        <span className="font-medium text-foreground">Reachable from the internet?</span> Probes the gateway's HTTP/HTTPS
        ports from external locations (via check-host.net). Needed for Let's Encrypt and for anyone outside your network.
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <Input
          className="h-8 max-w-xs font-mono text-xs"
          placeholder="auto-detect this node's public IP"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
        />
        <Button size="sm" variant="outline" onClick={run} disabled={check.isPending}>
          <Radar className={cn('mr-1 h-3.5 w-3.5', check.isPending && 'animate-spin')} />
          {check.isPending ? 'Probing… (~10 s)' : 'Check from the internet'}
        </Button>
        <span className="text-muted-foreground">
          Leave empty to use the IP this node goes out with; enter your real public IP or a route's domain if the node's
          traffic leaves through a VPN / tunnel.
        </span>
      </div>
      {r && (
        <div className="mt-3 space-y-2">
          {r.error ? (
            <div className="text-red-400">{r.error}</div>
          ) : (
            <>
              <div className="text-muted-foreground">
                {r.detected ? 'Detected public IP' : 'Probed'}{' '}
                <span className="font-mono text-foreground">{r.public_ip}</span>
                {r.detected && r.candidates && new Set(Object.values(r.candidates)).size > 1 && (
                  <div className="mt-0.5 text-amber-500">
                    IP echo services disagree — this node's traffic leaves through different paths (VPN / proxy with
                    per-destination routing):{' '}
                    {Object.entries(r.candidates)
                      .map(([svc, ip]) => `${svc} → ${ip}`)
                      .join(', ')}
                    . If the probed address isn't your gateway, enter the right one above.
                  </div>
                )}
              </div>
              {r.ports.map((p) => (
                <div key={p.port}>
                  <div className="flex items-center gap-1.5">
                    <span className={cn('h-2 w-2 rounded-full', p.reachable ? 'bg-emerald-400' : 'bg-red-400')} />
                    <span className="font-mono">:{p.port}</span>
                    <span className={p.reachable ? 'text-emerald-500' : 'text-red-400'}>
                      {p.reachable ? 'open from the internet' : 'not reachable from the internet'}
                    </span>
                  </div>
                  <div className="ml-3.5 mt-0.5 flex flex-wrap gap-x-4 gap-y-0.5 text-muted-foreground">
                    {p.probes.map((pr) => (
                      <span key={pr.node} title={pr.error}>
                        {pr.location || pr.node}: {pr.ok ? `ok ${pr.time_ms ? Math.round(pr.time_ms) + ' ms' : ''}` : pr.error || 'failed'}
                      </span>
                    ))}
                  </div>
                </div>
              ))}
              {r.ports.some((p) => !p.reachable) && (
                <div className="text-amber-500">
                  Closed ports usually mean a missing port-forward on the router / firewall rule, or a provider
                  blocking inbound traffic. If the public IP above isn't your gateway's (VPN, CGNAT), the probe hit the
                  wrong address.
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}

function StatusLine({ state }: { state: GatewayState }) {
  const st = state.status
  const cfg = state.config
  if (!cfg.enabled) {
    return <span className="text-xs text-muted-foreground">Gateway is off — routes are stored but not served.</span>
  }
  if (!st.is_gateway_node) {
    return (
      <span className="text-xs text-muted-foreground">
        Rendered on <b>{cfg.node_name || cfg.node_mac}</b>. Open that node's dashboard for its runtime status.
      </span>
    )
  }
  const ctrl = st.controller
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      <span className="inline-flex items-center gap-1.5">
        <span className={cn('h-2 w-2 rounded-full', st.last_error ? 'bg-red-400' : 'bg-emerald-400')} />
        This node is the gateway · {st.route_count} active route{st.route_count === 1 ? '' : 's'} · rendered{' '}
        {fmtTime(st.last_render_at)}
      </span>
      {st.file_path && (
        <code className="truncate" title={st.blocks_file_path ? `${st.file_path} + ${st.blocks_file_path}` : st.file_path}>
          {st.file_path}
          {st.blocks_file_path ? ` + ${st.blocks_file_path.split('/').pop()}` : ''}
        </code>
      )}
      {cfg.mode === 'managed' && (
        <span className="inline-flex items-center gap-1.5">
          <Activity className="h-3 w-3" />
          Traefik:{' '}
          {st.traefik_healthy === true ? (
            <span className="text-emerald-500">healthy</span>
          ) : st.traefik_healthy === false ? (
            <span className="text-amber-500" title={st.traefik_detail}>
              {ctrl?.phase === 'applying' ? 'starting…' : st.traefik_detail || 'not responding'}
            </span>
          ) : (
            'unknown'
          )}
        </span>
      )}
      {ctrl?.phase === 'error' && (
        <span className="text-red-400" title={ctrl.error}>
          controller: {ctrl.message} — {ctrl.error}
        </span>
      )}
      {st.last_error && <span className="text-red-400">{st.last_error}</span>}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Route form
// ---------------------------------------------------------------------------

const emptyRequest: RouteRequest = {
  name: '',
  domain: '',
  path_prefix: '',
  target_scheme: 'http',
  target_host: '',
  target_port: 0,
  target_host_mac: '',
  target_label: '',
  target_insecure_skip_verify: false,
  tls: true,
  mode: 'http',
  target_https_port: 443,
  basic_auth: [],
  ip_allow_list: '',
  ...DEFAULT_ROUTE_LIMITS,
  ...EMPTY_ROUTE_FEATURES,
  enabled: true,
}

const READ_TIMEOUT_PRESETS: { value: number; label: string }[] = [
  { value: 60, label: '1 minute (Traefik default — fails any slower upload)' },
  { value: 600, label: '10 minutes' },
  { value: 3600, label: '1 hour' },
  { value: 0, label: '24 hours (node-stats default)' },
  { value: -1, label: 'No limit' },
]

const MB = 1024 * 1024

function fmtLimits(r: GatewayRoute): string {
  const parts: string[] = []
  if (r.aliases) parts.push(`+${r.aliases.split(',').filter(Boolean).length} alias`)
  if (r.extra_targets) parts.push(`${r.extra_targets.split(/[\n,]/).filter((s) => s.trim()).length + 1} upstreams${r.health_check_path ? ' · health check' : ''}${r.sticky ? ' · sticky' : ''}`)
  if (r.strip_prefix && r.path_prefix) parts.push('strip prefix')
  if (r.add_prefix) parts.push(`+${r.add_prefix}`)
  if (r.host_header_mode === 'upstream' || r.host_header_mode === 'custom') parts.push(`host: ${r.host_header_mode}`)
  if (r.forward_auth_url) parts.push('forward auth')
  if (r.security_headers) parts.push('security headers')
  if (r.hsts && r.tls) parts.push('hsts')
  if (r.compress) parts.push('compress')
  if (r.max_conns_per_ip > 0) parts.push(`${r.max_conns_per_ip} concurrent/ip`)
  if (r.rate_limit_rps > 0) parts.push(`${r.rate_limit_rps} req/s/ip`)
  if (r.upstream_timeout_seconds > 0) parts.push(`${r.upstream_timeout_seconds}s upstream`)
  if (r.read_only) parts.push('read-only')
  if (r.max_body_bytes > 0) parts.push(`≤ ${Math.round(r.max_body_bytes / MB)} MB body`)
  return parts.join(' · ')
}

function targetKey(t: GatewayTarget) {
  return `${t.ipv4}:${t.port}`
}

function RouteForm({
  initial,
  routeId,
  onDone,
  state,
}: {
  initial: RouteRequest
  routeId?: string
  onDone: () => void
  state: GatewayState
}) {
  const { data: hostsData } = useHosts()
  const gatewayHost = (hostsData?.hosts ?? []).find(
    (h) => h.mac_address && h.mac_address.toLowerCase() === state.config.node_mac.toLowerCase()
  )
  const gwIP = gatewayHost?.ipv4 ?? ''
  const gwPort = state.config.mode === 'managed' ? state.config.http_port || 80 : 80
  const [req, setReq] = useState<RouteRequest>(initial)
  const [manual, setManual] = useState((!!initial.target_host && !initial.target_label) || initial.mode === 'passthrough')
  const { data: targets = [] } = useGatewayTargets()
  const create = useCreateRoute()
  const update = useUpdateRoute()
  const check = useCheckTarget()
  const pending = create.isPending || update.isPending

  const set = (patch: Partial<RouteRequest>) => setReq((r) => ({ ...r, ...patch }))
  const isPassthrough = req.mode === 'passthrough'
  const isRedirect = req.mode === 'redirect'
  const isStream = req.mode === 'stream'
  const isHTTP = req.mode === 'http'
  const canSubmit =
    (isStream ? req.listen_port > 0 && !!req.target_host && !!req.target_port : !!req.domain) &&
    (isRedirect ? !!req.redirect_url.trim() : isStream || (!!req.target_host && !!req.target_port))

  const selectedTarget = targets.find((t) => targetKey(t) === `${req.target_host}:${req.target_port}`)

  const pickTarget = (key: string) => {
    const t = targets.find((x) => targetKey(x) === key)
    if (!t) return
    set({
      target_host: t.ipv4,
      target_port: t.port,
      target_host_mac: t.host_mac,
      target_label: `${t.app}${t.container && t.container !== t.app ? ` · ${t.container}` : ''} @ ${t.host_name}`,
      name: req.name || t.app,
    })
  }

  const submit = () => {
    const body: RouteRequest = { ...req, target_port: Number(req.target_port) }
    const opts = {
      onSuccess: () => {
        toast.success(routeId ? 'Route updated' : 'Route added')
        onDone()
      },
      onError: (e: Error) => toast.error(e.message),
    }
    if (routeId) update.mutate({ routeId, req: body }, opts)
    else create.mutate(body, opts)
  }

  // Live reachability: re-check (debounced) whenever the target changes.
  const [reach, setReach] = useState<(TargetCheck & { key: string }) | null>(null)
  const [schemeNote, setSchemeNote] = useState<string>('')
  const [reachNonce, setReachNonce] = useState(0)
  const checkMutate = check.mutate
  const reachTimer = useRef<number | null>(null)
  useEffect(() => {
    const host = req.target_host.trim()
    const port = Number(req.target_port)
    if (!host || !port) {
      setReach(null)
      return
    }
    const key = `${host}:${port}`
    const mac = req.target_host_mac
    if (reachTimer.current) window.clearTimeout(reachTimer.current)
    reachTimer.current = window.setTimeout(() => {
      checkMutate(
        { host, host_mac: mac, port },
        {
          onSuccess: (r) => {
            setReach({ ...r, key })
            // Auto-detect the upstream protocol: a TLS handshake means the
            // service speaks HTTPS on that port (plain http would get Apache's
            // "You're speaking plain HTTP to an SSL-enabled server port").
            setReq((cur) => {
              if (!r.reachable) return cur
              if (r.tls && cur.target_scheme !== 'https') {
                setSchemeNote(
                  `Upstream speaks HTTPS${r.cert_subject ? ` (cert: ${r.cert_subject})` : ''} — switched the target to https${
                    r.cert_trusted ? '' : ' and enabled "skip upstream cert verification" (self-signed)'
                  }.`
                )
                return { ...cur, target_scheme: 'https', target_insecure_skip_verify: !r.cert_trusted }
              }
              if (!r.tls && cur.target_scheme === 'https') {
                setSchemeNote('Upstream does not speak TLS on this port — switched the target to http.')
                return { ...cur, target_scheme: 'http', target_insecure_skip_verify: false }
              }
              return cur
            })
          },
          onError: (e) => setReach({ checked: key, reachable: false, tls: false, cert_trusted: false, error: e.message, key }),
        }
      )
    }, 500)
    return () => {
      if (reachTimer.current) window.clearTimeout(reachTimer.current)
    }
  }, [req.target_host, req.target_host_mac, req.target_port, checkMutate, reachNonce])
  const reachCurrent = reach && reach.key === `${req.target_host.trim()}:${Number(req.target_port)}` ? reach : null
  useEffect(() => setSchemeNote(''), [req.target_host, req.target_port])
  const storedAddr = `${req.target_host.trim()}:${Number(req.target_port)}`
  const checkedDiffers = !!reachCurrent?.checked && reachCurrent.checked !== storedAddr

  const setAuth = (i: number, patch: Partial<BasicAuthInput>) =>
    set({ basic_auth: req.basic_auth.map((a, j) => (j === i ? { ...a, ...patch } : a)) })

  return (
    <div className="space-y-4 rounded-lg border border-border/60 bg-muted/10 p-4">
      <div className="space-y-1.5">
        <Label>Route type</Label>
        <select
          className={selectCls}
          value={req.mode}
          onChange={(e) => {
            const mode = e.target.value as RouteRequest['mode']
            const noLimits = { max_conns_per_ip: 0, rate_limit_rps: 0, read_only: false, upstream_timeout_seconds: 0, max_body_bytes: 0 }
            const noHTTP = { path_prefix: '', basic_auth: [], ip_allow_list: '', ...noLimits, ...EMPTY_ROUTE_FEATURES, aliases: req.aliases }
            switch (mode) {
              case 'passthrough':
                set({ mode, tls: false, target_scheme: 'http', target_port: req.target_port || 80, target_https_port: req.target_https_port || 443, ...noHTTP })
                break
              case 'redirect':
                set({ mode, tls: true, target_scheme: 'http', ...noHTTP, redirect_permanent: true, redirect_preserve_path: true })
                break
              case 'stream':
                setManual(true)
                set({ mode, tls: false, target_scheme: 'http', ...noHTTP, aliases: '', protocol: 'tcp', listen_port: req.listen_port || req.target_port || 0 })
                break
              default:
                set({ mode, tls: true, ...(routeId ? {} : DEFAULT_ROUTE_LIMITS), redirect_url: '', protocol: '', listen_port: 0 })
            }
          }}
        >
          <option value="http">Publish a service — this gateway terminates TLS and proxies HTTP</option>
          <option value="passthrough">Delegate to another reverse proxy — TLS passthrough (it issues its own certificates)</option>
          <option value="redirect">Redirect — answer the hostname(s) with a redirect to another URL</option>
          <option value="stream">Forward a TCP/UDP port — raw stream (game servers, SSH, databases)</option>
        </select>
        {isPassthrough && (
          <p className="text-xs text-muted-foreground">
            :443 traffic for the domain is forwarded as raw TLS by SNI to the other proxy (Traefik / Caddy / NPM…) which
            terminates it and runs its own ACME; :80 is proxied to its http port so its HTTP-01 challenges and redirects
            work. Use a wildcard (<span className="font-mono">*.apps.example.com</span>) to hand it a whole subdomain
            space.
          </p>
        )}
        {isRedirect && (
          <p className="text-xs text-muted-foreground">
            No upstream: every request for the domain (and aliases) gets a 301/302 to the URL below. With HTTPS on, the
            old name still gets a certificate so <span className="font-mono">https://old…</span> redirects cleanly too.
          </p>
        )}
        {isStream && (
          <p className="text-xs text-muted-foreground">
            The gateway node publishes the port and forwards the raw stream to the target — no hostname, no TLS, no HTTP
            features. Each new port restarts the managed Traefik (a new listener); the port must be free on the gateway
            host and open in its firewall. Client blocks don't apply to streams.
          </p>
        )}
      </div>
      {isStream ? (
      <div className="grid gap-4 md:grid-cols-[160px_1fr]">
        <div className="space-y-1.5">
          <Label>Protocol</Label>
          <select className={selectCls} value={req.protocol || 'tcp'} onChange={(e) => set({ protocol: e.target.value === 'udp' ? 'udp' : 'tcp' })}>
            <option value="tcp">tcp</option>
            <option value="udp">udp</option>
          </select>
        </div>
        <div className="space-y-1.5">
          <Label>Public port on the gateway node</Label>
          <Input type="number" min={1} max={65535} placeholder="25565" value={req.listen_port || ''} onChange={(e) => set({ listen_port: Number(e.target.value) || 0 })} />
          <p className="text-xs text-muted-foreground">
            Clients connect to <span className="font-mono">{gwIP || '<gateway ip>'}:{req.listen_port || '…'}</span>
            {req.protocol === 'udp' ? ' (udp)' : ''}. Can't be the gateway's http/https ports.
          </p>
        </div>
      </div>
      ) : (
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Domain</Label>
          <Input
            placeholder={isPassthrough ? '*.apps.example.com' : 'grafana.example.com'}
            value={req.domain}
            onChange={(e) => set({ domain: e.target.value })}
          />
          <p className="text-xs text-muted-foreground">
            {req.domain ? (
              <>
                Public URL: <span className="font-mono">{`${req.tls ? 'https' : 'http'}://${req.domain.trim()}${
                  req.tls ? (state.config.mode === 'managed' && (state.config.https_port || 443) !== 443 ? `:${state.config.https_port}` : '') : gwPort !== 80 ? `:${gwPort}` : ''
                }${firstPrefix(req.path_prefix)}`}</span>
                {' — '}
              </>
            ) : null}
            its DNS must point at the gateway node
            {gwIP ? <> (<span className="font-mono">{gwIP}</span>)</> : null}.
            {gwIP ? (
              <>
                {' '}
                No DNS yet? Use the gateway IP itself as the domain to test:{' '}
                <button type="button" className="font-mono underline" onClick={() => set({ domain: gwIP })}>
                  {gwIP}
                </button>{' '}
                → http://{gwIP}
                {gwPort !== 80 ? `:${gwPort}` : ''}
              </>
            ) : null}
          </p>
        </div>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>Aliases (optional — extra hostnames, comma-separated)</Label>
            <Input placeholder="www.example.com, example.net" value={req.aliases} onChange={(e) => set({ aliases: e.target.value })} />
            <p className="text-xs text-muted-foreground">Served by the same route; each lands as a SAN on the same certificate.</p>
          </div>
          {isHTTP && (
            <div className="space-y-1.5">
              <Label>Path prefix (optional — several: comma-separated)</Label>
              <Input placeholder="/ or /api, /oauth2" value={req.path_prefix} onChange={(e) => set({ path_prefix: e.target.value })} />
            </div>
          )}
        </div>
      </div>
      )}

      {isRedirect && (
      <div className="space-y-2">
        <Label>Redirect to</Label>
        <Input placeholder="https://new.example.com/" value={req.redirect_url} onChange={(e) => set({ redirect_url: e.target.value })} />
        <div className="flex flex-wrap gap-6 text-sm">
          <label className="inline-flex items-center gap-2">
            <Switch checked={req.redirect_permanent} onCheckedChange={(v) => set({ redirect_permanent: v })} />
            Permanent (301) — browsers cache it; use 302 while testing
          </label>
          <label className="inline-flex items-center gap-2">
            <Switch checked={req.redirect_preserve_path} onCheckedChange={(v) => set({ redirect_preserve_path: v })} />
            Keep the request path (<span className="font-mono">/x/y?q</span> → <span className="font-mono">{(req.redirect_url || 'https://new…').replace(/\/$/, '')}/x/y?q</span>)
          </label>
        </div>
      </div>
      )}

      {!isRedirect && (
      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <Label>Target</Label>
          <button type="button" className="text-xs text-muted-foreground underline" onClick={() => setManual((m) => !m)}>
            {manual ? 'pick a detected service' : 'enter manually'}
          </button>
        </div>
        {!manual ? (
          <select
            className={selectCls}
            value={selectedTarget ? targetKey(selectedTarget) : ''}
            onChange={(e) => pickTarget(e.target.value)}
          >
            <option value="">— detected services with a published port —</option>
            {targets.map((t) => (
              <option key={targetKey(t)} value={targetKey(t)}>
                {t.host_name} · {t.app}
                {t.container && t.container !== t.app ? ` (${t.container})` : ''} · {t.ipv4}:{t.port}
                {t.private_port && t.private_port !== t.port ? ` → :${t.private_port}` : ''}
              </option>
            ))}
          </select>
        ) : isStream ? (
          <div className="grid gap-2 md:grid-cols-[1fr_120px]">
            <Input
              placeholder="10.0.0.5 or container name"
              value={req.target_host}
              onChange={(e) => set({ target_host: e.target.value, target_label: '', target_host_mac: '' })}
            />
            <Input type="number" placeholder="port" value={req.target_port || ''} onChange={(e) => set({ target_port: Number(e.target.value) })} />
          </div>
        ) : (
          isPassthrough ? (
          <div className="grid gap-2 md:grid-cols-[1fr_140px_140px]">
            <Input
              placeholder="other proxy IP or hostname"
              value={req.target_host}
              onChange={(e) => set({ target_host: e.target.value, target_label: '', target_host_mac: '' })}
            />
            <Input
              type="number"
              placeholder="http port (80)"
              value={req.target_port || ''}
              onChange={(e) => set({ target_port: Number(e.target.value) })}
            />
            <Input
              type="number"
              placeholder="https port (443)"
              value={req.target_https_port || ''}
              onChange={(e) => set({ target_https_port: Number(e.target.value) })}
            />
          </div>
          ) : (
          <div className="grid gap-2 md:grid-cols-[110px_1fr_120px]">
            <select
              className={selectCls}
              value={req.target_scheme}
              onChange={(e) => set({ target_scheme: e.target.value })}
            >
              <option value="http">http</option>
              <option value="https">https</option>
              <option value="h2c">h2c (gRPC)</option>
            </select>
            <Input
              placeholder="10.0.0.5 or hostname"
              value={req.target_host}
              onChange={(e) => set({ target_host: e.target.value, target_label: '', target_host_mac: '' })}
            />
            <Input
              type="number"
              placeholder="port"
              value={req.target_port || ''}
              onChange={(e) => set({ target_port: Number(e.target.value) })}
            />
          </div>
          )
        )}
        <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
          {req.target_host && req.target_port ? (
            <span className="font-mono">
              → {req.target_scheme}://{checkedDiffers && reachCurrent?.checked ? reachCurrent.checked : `${req.target_host}:${req.target_port}`}
              {checkedDiffers ? <span className="font-sans text-muted-foreground"> (stored as {req.target_host}:{req.target_port})</span> : null}
            </span>
          ) : (
            <span>Pick a detected service or enter an address reachable from the gateway node.</span>
          )}
          {req.target_host && req.target_port ? (
            <button
              type="button"
              className="inline-flex items-center gap-1.5 text-left hover:text-foreground"
              title={(reachCurrent?.error ? reachCurrent.error + ' · ' : '') + 'click to re-check'}
              onClick={() => setReachNonce((n) => n + 1)}
            >
              <span
                className={cn(
                  'h-2 w-2 rounded-full',
                  !reachCurrent || check.isPending ? 'bg-zinc-400 animate-pulse' : reachCurrent.reachable ? 'bg-emerald-400' : 'bg-red-400'
                )}
              />
              {!reachCurrent || check.isPending
                ? 'checking from this node…'
                : reachCurrent.reachable
                  ? `reachable from this node · ${reachCurrent.tls ? `TLS${reachCurrent.cert_trusted ? '' : ' (self-signed)'}` : 'plain http'}`
                  : `unreachable from this node${reachCurrent.error ? ` — ${reachCurrent.error}` : ''}`}
            </button>
          ) : null}
          {schemeNote && <span className="text-amber-500">{schemeNote}</span>}
          {req.target_scheme === 'h2c' && (
            <span>
              HTTP/2 cleartext to the upstream — required for gRPC services (NetBird management/signal, Grafana Tempo…);
              plain HTTP/1.1 upstreams break on it, so put WebSocket/REST paths on a separate <b>http</b> route.
            </span>
          )}

          {req.target_scheme === 'https' && (
            <label className="inline-flex items-center gap-1.5">
              <input
                type="checkbox"
                checked={req.target_insecure_skip_verify}
                onChange={(e) => set({ target_insecure_skip_verify: e.target.checked })}
              />
              skip upstream cert verification
            </label>
          )}
        </div>
        {(isHTTP || isStream) && (
          <div className="space-y-1.5 pt-1">
            <Label className="text-xs">More upstreams (optional — host:port per line; same {isStream ? 'protocol' : 'scheme'})</Label>
            <textarea
              className={textareaCls}
              rows={2}
              placeholder={'10.0.0.6:8443\n10.0.0.7:8443'}
              value={req.extra_targets}
              onChange={(e) => set({ extra_targets: e.target.value })}
            />
            <p className="text-[11px] text-muted-foreground">
              Requests are round-robined over all upstreams — the same app on several cluster nodes.
              {isHTTP ? ' Add a health check below so a failing node is taken out automatically.' : ''}
            </p>
          </div>
        )}
      </div>
      )}

      {isHTTP && (
      <div className="space-y-3 rounded-lg border border-border/60 p-3">
        <div>
          <div className="text-sm font-medium">Upstream options</div>
          <p className="text-xs text-muted-foreground">How the request is reshaped on its way to the service.</p>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="space-y-1">
            <Label className="text-xs">Host header sent to the upstream</Label>
            <select className={selectCls} value={req.host_header_mode || 'client'} onChange={(e) => set({ host_header_mode: e.target.value as RouteRequest['host_header_mode'] })}>
              <option value="client">Client's Host (default — {req.domain || 'the public domain'})</option>
              <option value="upstream">Upstream's own host:port (passHostHeader off)</option>
              <option value="custom">Custom value</option>
            </select>
            {req.host_header_mode === 'custom' && (
              <Input placeholder="app.internal" value={req.host_header_value} onChange={(e) => set({ host_header_value: e.target.value })} />
            )}
            <p className="text-[11px] text-muted-foreground">Change it for upstreams that route by their own vhost (another proxy, a tunnel, S3-style storage).</p>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Path rewrite</Label>
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={req.strip_prefix} disabled={!req.path_prefix.trim() || req.path_prefix.trim() === '/'} onCheckedChange={(v) => set({ strip_prefix: v })} />
              Strip the path prefix{req.path_prefix.trim() && req.path_prefix.trim() !== '/' ? ` (${req.path_prefix.trim()} → /)` : ' (needs a path prefix)'}
            </label>
            <Input placeholder="add prefix, e.g. /app (optional)" value={req.add_prefix} onChange={(e) => set({ add_prefix: e.target.value })} />
          </div>
          {req.target_scheme === 'https' && (
            <div className="space-y-1">
              <Label className="text-xs">TLS server name (SNI) for the upstream</Label>
              <Input placeholder={req.target_host || 'app.internal'} value={req.target_server_name} onChange={(e) => set({ target_server_name: e.target.value })} />
              <p className="text-[11px] text-muted-foreground">When the upstream is addressed by IP but serves a certificate for a name.</p>
            </div>
          )}
        </div>
        {req.extra_targets.trim() && (
          <div className="grid gap-3 md:grid-cols-3">
            <div className="space-y-1">
              <Label className="text-xs">Health-check path (optional)</Label>
              <Input placeholder="/healthz" value={req.health_check_path} onChange={(e) => set({ health_check_path: e.target.value })} />
              <p className="text-[11px] text-muted-foreground">An upstream answering non-2xx/3xx is skipped until it recovers.</p>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Health-check interval (s)</Label>
              <Input type="number" min={0} disabled={!req.health_check_path.trim()} value={req.health_check_interval_seconds || ''} placeholder="10" onChange={(e) => set({ health_check_interval_seconds: Math.max(0, Number(e.target.value) || 0) })} />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Retry on another upstream</Label>
              <Input type="number" min={0} max={10} value={req.retry_attempts} onChange={(e) => set({ retry_attempts: Math.max(0, Math.min(10, Number(e.target.value) || 0)) })} />
              <label className="flex items-center gap-2 pt-1 text-sm">
                <Switch checked={req.sticky} onCheckedChange={(v) => set({ sticky: v })} />
                Sticky sessions (cookie)
              </label>
            </div>
          </div>
        )}
      </div>
      )}

      {!isStream && (
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Name (optional)</Label>
          <Input placeholder={req.domain || 'Grafana'} value={req.name} onChange={(e) => set({ name: e.target.value })} />
        </div>
        {!isPassthrough && (
          <div className="flex items-end gap-2 pb-1">
            <Switch checked={req.tls} onCheckedChange={(v) => set({ tls: v })} />
            <span className="text-sm">{isRedirect ? 'HTTPS too (certificate for the old name; http redirects as well)' : 'HTTPS (TLS terminated on the gateway; http redirects)'}</span>
          </div>
        )}
      </div>
      )}
      {isStream && (
        <div className="space-y-1.5">
          <Label>Name (optional)</Label>
          <Input placeholder={`${req.protocol || 'tcp'}/${req.listen_port || '…'}`} value={req.name} onChange={(e) => set({ name: e.target.value })} />
        </div>
      )}

      {isHTTP && (
      <div className="space-y-2 rounded-lg border border-border/60 p-3">
        <div className="flex items-center justify-between">
          <div>
            <div className="flex items-center gap-1.5 text-sm font-medium">
              <Lock className="h-3.5 w-3.5" /> Access control
            </div>
            <p className="text-xs text-muted-foreground">
              Without any of these the service is open to the whole internet. Basic auth prompts in the browser; the
              allow list restricts by client IP/CIDR.
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => set({ basic_auth: [...req.basic_auth, { user: '', password: '' }] })}
          >
            <Plus className="mr-1 h-3.5 w-3.5" /> user
          </Button>
        </div>
        {req.basic_auth.map((a, i) => (
          <div key={i} className="grid gap-2 md:grid-cols-[1fr_1fr_36px]">
            <Input placeholder="user" value={a.user} onChange={(e) => setAuth(i, { user: e.target.value })} />
            <Input
              type="password"
              placeholder={routeId && initial.basic_auth.some((b) => b.user === a.user) ? '(unchanged)' : 'password'}
              value={a.password}
              onChange={(e) => setAuth(i, { password: e.target.value })}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => set({ basic_auth: req.basic_auth.filter((_, j) => j !== i) })}
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
        ))}
        <div className="space-y-1.5">
          <Label className="text-xs">IP allow list (comma-separated CIDRs, empty = everyone)</Label>
          <Input
            placeholder="10.0.0.0/8, 203.0.113.7"
            value={req.ip_allow_list}
            onChange={(e) => set({ ip_allow_list: e.target.value })}
          />
        </div>
        <div className="space-y-1.5 border-t border-border/60 pt-2">
          <Label className="text-xs">Forward auth / SSO (optional — Authelia, Authentik, Pocket-ID…)</Label>
          <Input
            placeholder="http://authelia:9091/api/authz/forward-auth"
            value={req.forward_auth_url}
            onChange={(e) => set({ forward_auth_url: e.target.value })}
          />
          {req.forward_auth_url.trim() && (
            <div className="grid gap-2 md:grid-cols-[1fr_auto]">
              <Input
                placeholder="headers copied to the upstream: Remote-User, Remote-Groups, Remote-Email"
                value={req.forward_auth_response_headers}
                onChange={(e) => set({ forward_auth_response_headers: e.target.value })}
              />
              <label className="inline-flex items-center gap-2 text-sm">
                <Switch checked={req.forward_auth_trust_forward_header} onCheckedChange={(v) => set({ forward_auth_trust_forward_header: v })} />
                trust X-Forwarded-*
              </label>
            </div>
          )}
          <p className="text-[11px] text-muted-foreground">
            Every request is first sent to this address; a non-2xx answer (the login page) goes back to the client, a 2xx
            lets it through. Reachable from the gateway node — a container name works when its network is attached.
          </p>
        </div>
      </div>
      )}

      {isHTTP && (
      <div className="space-y-3 rounded-lg border border-border/60 p-3">
        <div>
          <div className="text-sm font-medium">Headers & compression</div>
          <p className="text-xs text-muted-foreground">Response hardening for browsers, custom headers in both directions, gzip/brotli.</p>
        </div>
        <div className="grid gap-3 md:grid-cols-3">
          <label className="flex items-start gap-2">
            <Switch checked={req.security_headers} onCheckedChange={(v) => set({ security_headers: v })} />
            <span className="text-sm">
              Security headers
              <span className="block text-[11px] text-muted-foreground">X-Frame-Options SAMEORIGIN, nosniff, XSS filter, referrer policy.</span>
            </span>
          </label>
          <label className={cn('flex items-start gap-2', !req.tls && 'opacity-50')}>
            <Switch checked={req.hsts && req.tls} disabled={!req.tls} onCheckedChange={(v) => set({ hsts: v })} />
            <span className="text-sm">
              HSTS (2 years)
              <span className="block text-[11px] text-muted-foreground">
                {req.tls ? 'Browsers refuse plain http for this name afterwards.' : 'Needs HTTPS.'}
                {req.hsts && req.tls && (
                  <label className="mt-1 flex items-center gap-1.5">
                    <input type="checkbox" checked={req.hsts_include_subdomains} onChange={(e) => set({ hsts_include_subdomains: e.target.checked })} /> include subdomains
                  </label>
                )}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2">
            <Switch checked={req.compress} onCheckedChange={(v) => set({ compress: v })} />
            <span className="text-sm">
              Compress responses
              <span className="block text-[11px] text-muted-foreground">gzip / brotli / zstd for text-like content the app didn't compress itself.</span>
            </span>
          </label>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="space-y-1">
            <Label className="text-xs">Request headers to the upstream (Name: value per line)</Label>
            <textarea className={textareaCls} rows={2} placeholder={'X-Forwarded-Prefix: /grafana'} value={req.request_headers} onChange={(e) => set({ request_headers: e.target.value })} />
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Response headers to the client (Name: value per line)</Label>
            <textarea className={textareaCls} rows={2} placeholder={'X-Robots-Tag: noindex'} value={req.response_headers} onChange={(e) => set({ response_headers: e.target.value })} />
          </div>
        </div>
      </div>
      )}

      {isHTTP && (
      <div className="space-y-3 rounded-lg border border-border/60 p-3">
        <div>
          <div className="flex items-center gap-1.5 text-sm font-medium">
            <Gauge className="h-3.5 w-3.5" /> Request limits
          </div>
          <p className="text-xs text-muted-foreground">
            Per-route guards against connection hoarding and floods. The gateway-wide read timeout can't be scoped to a
            route, so routes bound abuse by concurrency, rate, method and size instead. 0 = off; loosen for file
            services, tighten for small admin UIs.
          </p>
        </div>
        <div className="grid gap-3 md:grid-cols-3">
          <div className="space-y-1">
            <Label className="text-xs">Max concurrent requests per IP</Label>
            <Input type="number" min={0} value={req.max_conns_per_ip} onChange={(e) => set({ max_conns_per_ip: Math.max(0, Number(e.target.value) || 0) })} />
            <p className="text-[11px] text-muted-foreground">429 above it. Stops one client holding hundreds of connections.</p>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Rate limit (requests/s per IP)</Label>
            <Input type="number" min={0} value={req.rate_limit_rps} onChange={(e) => set({ rate_limit_rps: Math.max(0, Number(e.target.value) || 0) })} />
            <p className="text-[11px] text-muted-foreground">Average per second, burst 2×; 429 above it.</p>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Upstream response timeout (s)</Label>
            <Input type="number" min={0} value={req.upstream_timeout_seconds} onChange={(e) => set({ upstream_timeout_seconds: Math.max(0, Number(e.target.value) || 0) })} />
            <p className="text-[11px] text-muted-foreground">504 when the service doesn't start answering in time. Raise for slow report/long-poll endpoints.</p>
          </div>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          <label className="flex items-start gap-2 pt-1">
            <Switch checked={req.read_only} onCheckedChange={(v) => set({ read_only: v })} />
            <span className="text-sm">
              Read-only (GET / HEAD / OPTIONS only)
              <span className="block text-[11px] text-muted-foreground">The service never receives a request body — for dashboards and status pages.</span>
            </span>
          </label>
          <div className="space-y-1">
            <Label className="text-xs">Max request body (MB, 0 = unlimited)</Label>
            <Input
              type="number"
              min={0}
              value={req.max_body_bytes > 0 ? Math.round(req.max_body_bytes / MB) : 0}
              onChange={(e) => set({ max_body_bytes: Math.max(0, Math.round(Number(e.target.value) || 0)) * MB })}
            />
            {req.max_body_bytes > 0 ? (
              <p className="text-[11px] text-amber-500">
                Traefik's buffering middleware holds the whole request <i>and</i> response before forwarding — it breaks
                SSE / streaming and slows big downloads. Use it for small admin UIs only, never for file services.
              </p>
            ) : (
              <p className="text-[11px] text-muted-foreground">413 above it (buffered, see warning when set). Leave 0 for file/media services.</p>
            )}
          </div>
        </div>
      </div>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone} disabled={pending}>
          Cancel
        </Button>
        <Button type="button" size="sm" onClick={submit} disabled={pending || !canSubmit}>
          {pending ? 'Saving…' : routeId ? 'Save route' : 'Add route'}
        </Button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Route list
// ---------------------------------------------------------------------------

function RouteRow({ route, onEdit }: { route: GatewayRoute; onEdit: () => void }) {
  const update = useUpdateRoute()
  const del = useDeleteRoute()

  const toggle = (enabled: boolean) =>
    update.mutate(
      { routeId: route.route_id, req: { ...routeToRequest(route), enabled } },
      { onError: (e) => toast.error('Toggle failed: ' + e.message) }
    )

  const onDelete = async () => {
    const { confirmed } = await confirmDialog({
      title: 'Remove route?',
      description: `${route.domain}${route.path_prefix} will stop being served by the gateway.`,
      variant: 'destructive',
      confirmText: 'Remove',
    })
    if (!confirmed) return
    del.mutate(route.route_id, {
      onSuccess: () => toast.success('Route removed'),
      onError: (e) => toast.error('Remove failed: ' + e.message),
    })
  }

  return (
    <div className="px-4 py-3 transition-colors hover:bg-muted/20">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          {/* 1. name + badges */}
          <div className="flex flex-wrap items-center gap-2">
            <span className={cn('truncate font-medium', !route.enabled && 'text-muted-foreground line-through')}>
              {route.name || route.domain}
            </span>
            {route.mode === 'stream' ? (
              <Badge variant="secondary" className="text-[10px] uppercase" title="raw TCP/UDP forward">
                {route.protocol || 'tcp'} stream
              </Badge>
            ) : route.mode === 'redirect' ? (
              <Badge variant="secondary" className="text-[10px] uppercase" title="redirect — no upstream">
                redirect {route.redirect_permanent ? '301' : '302'}
              </Badge>
            ) : route.mode === 'passthrough' ? (
              <Badge variant="secondary" className="text-[10px] uppercase" title="TLS passthrough — the other proxy terminates TLS">
                passthrough
              </Badge>
            ) : route.tls ? (
              <Badge variant="secondary" className="text-[10px] uppercase">https</Badge>
            ) : (
              <Badge variant="outline" className="text-[10px] uppercase">http</Badge>
            )}
            {route.mode === 'passthrough' ? (
              <span className="text-xs text-muted-foreground">TLS + access control handled by the other proxy</span>
            ) : route.mode === 'stream' ? (
              <span className="text-xs text-muted-foreground">raw stream — secure the service itself</span>
            ) : route.mode === 'redirect' ? null : route.protected ? (
              <span className="inline-flex items-center gap-1 text-xs text-emerald-500" title="basic auth / IP allow list">
                <ShieldCheck className="h-3.5 w-3.5" /> protected
              </span>
            ) : (
              <span className="inline-flex items-center gap-1 text-xs text-amber-500" title="no basic auth or IP allow list">
                <ShieldAlert className="h-3.5 w-3.5" /> public
              </span>
            )}
          </div>
          {/* 2. the public address as configured (scheme + domain + gateway port) */}
          <div className="truncate font-mono text-xs">
            <a href={route.public_url} target="_blank" rel="noreferrer" className="hover:underline">
              {route.public_url}
            </a>
          </div>
          {/* 3. where it really goes */}
          <div className="truncate font-mono text-xs text-muted-foreground">
            → {route.mode === 'passthrough'
              ? `${route.target_host}:${route.target_https_port || 443} (tls, sni) · :${route.target_port || 80} (http)`
              : route.mode === 'redirect'
                ? `${route.redirect_url}${route.redirect_preserve_path ? ' (path kept)' : ''}`
                : route.mode === 'stream'
                  ? `${route.target_host}:${route.target_port}${route.extra_targets ? ` (+${route.extra_targets.split(/[\n,]/).filter((s) => s.trim()).length})` : ''}`
                  : route.effective_url || route.target_url || `${route.target_scheme}://${route.target_host}:${route.target_port}`}
            {route.rewritten ? (
              <span
                className="font-sans"
                title="The target lives on the gateway host itself, so Traefik (a container there) reaches it via host.docker.internal. The stored address is what gets rendered if the gateway moves to another node."
              >
                {' '}
                (stored as <span className="font-mono">{route.target_url}</span>)
              </span>
            ) : null}
            {route.target_label ? <span className="font-sans"> · {route.target_label}</span> : null}
          </div>
          {route.mode === 'http' && fmtLimits(route) ? (
            <div className="flex items-center gap-1 text-[11px] text-muted-foreground" title="Request limits (route form → Request limits)">
              <Gauge className="h-3 w-3" /> {fmtLimits(route)}
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <Switch checked={route.enabled} disabled={update.isPending} onCheckedChange={toggle} />
          <a
            href={route.public_url}
            target="_blank"
            rel="noreferrer"
            title="Open"
            className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <ExternalLink className="h-4 w-4" />
          </a>
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onEdit} title="Edit">
            <Pencil className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-8 w-8 text-red-400 hover:text-red-500" onClick={onDelete} title="Remove">
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Traefik logs (gateway node, managed mode)
// ---------------------------------------------------------------------------

function LogsCard({ state }: { state: GatewayState }) {
  const [open, setOpen] = useState(false)
  const [live, setLive] = useState(true)
  const [tail, setTail] = useState(300)
  const enabled = open && state.status.is_gateway_node && state.config.mode === 'managed'
  const { data, isLoading, isFetching, refetch } = useGatewayLogs(enabled, tail, live)

  if (!state.config.enabled || !state.status.is_gateway_node || state.config.mode !== 'managed') return null

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <ScrollText className="h-4 w-4" /> Traefik logs
            </CardTitle>
            <CardDescription>
              Service log + access log of the managed Traefik on this node
              {state.capabilities.manage_kind === 'systemd' ? ' (journalctl)' : ' (docker logs)'}, newest at the bottom.
              {live ? ' Refreshes every 5 s.' : ' Paused.'}
            </CardDescription>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {open && (
              <>
                <select className={cn(selectCls, 'h-8 w-auto')} value={tail} onChange={(e) => setTail(Number(e.target.value))}>
                  {[100, 300, 1000].map((n) => (
                    <option key={n} value={n}>
                      last {n}
                    </option>
                  ))}
                </select>
                <Button type="button" variant="outline" size="sm" onClick={() => setLive((v) => !v)} className="gap-1.5">
                  {live ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
                  {live ? 'Pause' : 'Live'}
                </Button>
                <Button type="button" variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching} className="gap-1.5">
                  <RefreshCw className={cn('h-3.5 w-3.5', isFetching && 'animate-spin')} />
                  Refresh
                </Button>
              </>
            )}
            <Button variant="outline" size="sm" onClick={() => setOpen((o) => !o)}>
              {open ? 'Hide' : 'Show'}
            </Button>
          </div>
        </div>
      </CardHeader>
      {open && (
        <CardContent>
          {data?.error && <div className="mb-2 text-xs text-amber-500">{data.error}</div>}
          <LogViewer logs={data?.logs ?? ''} loading={isLoading} />
        </CardContent>
      )}
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Config files (what node-stats writes for Traefik on this node)
// ---------------------------------------------------------------------------

function fmtSize(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

function ConfigFilesCard({ state }: { state: GatewayState }) {
  const [open, setOpen] = useState(false)
  const enabled = open && state.config.enabled && state.status.is_gateway_node
  const { data, isFetching, refetch } = useGatewayFiles(enabled)
  const files = data?.files ?? []
  const [selected, setSelected] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const current: GatewayConfigFile | undefined = files.find((f) => f.name === selected) ?? files[0]

  if (!state.config.enabled || !state.status.is_gateway_node) return null

  const copy = async () => {
    if (!current?.content) return
    try {
      await navigator.clipboard.writeText(current.content)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error('Clipboard unavailable')
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <FileCode2 className="h-4 w-4" /> Traefik config files
            </CardTitle>
            <CardDescription>
              Exactly what node-stats writes for Traefik on this node — the dynamic route file, the deny list
              {state.config.mode === 'managed' ? ', and the static config it generates for the managed Traefik' : ''}.
              Read-only: edits are overwritten on the next render.
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            {open && (
              <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => refetch()} title="Refresh">
                <RefreshCw className={cn('h-4 w-4', isFetching && 'animate-spin')} />
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => setOpen((o) => !o)}>
              {open ? 'Hide' : 'Show'}
            </Button>
          </div>
        </div>
      </CardHeader>
      {open && (
        <CardContent className="space-y-3">
          {data?.error && <div className="text-xs text-amber-500">{data.error}</div>}
          {files.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {files.map((f) => (
                <button
                  key={f.name}
                  type="button"
                  onClick={() => setSelected(f.name)}
                  className={cn(
                    'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 font-mono text-xs transition-colors',
                    current?.name === f.name
                      ? 'border-primary/60 bg-primary/10 text-foreground'
                      : 'border-border/60 text-muted-foreground hover:bg-muted/40 hover:text-foreground',
                    f.missing && 'opacity-60'
                  )}
                  title={f.path || f.note}
                >
                  {f.name}
                  <Badge variant="outline" className="text-[9px] uppercase">{f.kind}</Badge>
                  {f.missing && <span className="text-[10px] font-sans">absent</span>}
                </button>
              ))}
            </div>
          )}
          {current && (
            <>
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <div className="min-w-0 space-y-0.5">
                  {current.path && <div className="truncate font-mono">{current.path}</div>}
                  <div>
                    {current.missing
                      ? 'Not present right now.'
                      : `${fmtSize(current.size)}${current.modified ? ` · written ${fmtTime(current.modified)}` : ' · generated preview'}`}
                    {current.note ? ` — ${current.note}` : ''}
                  </div>
                </div>
                {!current.missing && current.content && (
                  <Button variant="ghost" size="sm" className="h-7" onClick={copy}>
                    {copied ? <Check className="mr-1 h-3.5 w-3.5 text-emerald-500" /> : <Copy className="mr-1 h-3.5 w-3.5" />}
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                )}
              </div>
              <pre className="max-h-[32rem] overflow-auto rounded-lg border border-border/60 bg-black/80 p-3 font-mono text-[11px] leading-snug text-zinc-200">
                {current.missing ? '(no file)' : current.content || (isFetching ? 'loading…' : '(empty)')}
              </pre>
            </>
          )}
          {files.length === 0 && !data?.error && <p className="text-sm text-muted-foreground">{isFetching ? 'loading…' : 'Nothing rendered yet.'}</p>}
        </CardContent>
      )}
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Tab
// ---------------------------------------------------------------------------

export function GatewayTab() {
  const { data, isLoading, error } = useGateway()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)

  if (isLoading || !data) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    )
  }
  if (error) {
    return <div className="text-sm text-red-400">Failed to load gateway state: {(error as Error).message}</div>
  }

  return (
    <div className="space-y-6">
      <ConfigCard state={data} />

      <Card>
        <CardHeader>
          <div className="flex items-start justify-between gap-3">
            <div>
              <CardTitle>Routes</CardTitle>
              <CardDescription>
                Public hostnames and where the gateway forwards them. Changes replicate across the cluster and are
                applied on the gateway node within seconds.
              </CardDescription>
            </div>
            {!adding && (
              <Button size="sm" onClick={() => { setAdding(true); setEditing(null) }}>
                <Plus className="mr-1 h-4 w-4" /> Add route
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent className="space-y-4 p-0">
          {adding && (
            <div className="px-4 pb-2">
              <RouteForm initial={emptyRequest} onDone={() => setAdding(false)} state={data} />
            </div>
          )}
          {data.routes.length === 0 && !adding ? (
            <p className="px-4 pb-4 text-sm text-muted-foreground">
              No routes yet. Add one to publish a detected service under a domain.
            </p>
          ) : (
            <div className="divide-y divide-border/60">
              {data.routes.map((r) =>
                editing === r.route_id ? (
                  <div key={r.route_id} className="p-4">
                    <RouteForm initial={routeToRequest(r)} routeId={r.route_id} onDone={() => setEditing(null)} state={data} />
                  </div>
                ) : (
                  <RouteRow key={r.route_id} route={r} onEdit={() => { setEditing(r.route_id); setAdding(false) }} />
                )
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <ConnectionsCard state={data} />

      <BlocksCard state={data} />

      <LogsCard state={data} />

      <ConfigFilesCard state={data} />
    </div>
  )
}
