import { useEffect, useMemo, useRef, useState } from 'react'
import { format } from 'date-fns'
import { toast } from 'sonner'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { Globe, Plus, Trash2, Pencil, ExternalLink, ShieldAlert, ShieldCheck, Lock, Activity, X, ScrollText, RefreshCw } from 'lucide-react'
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
  routeToRequest,
} from './useGateway'
import type { GatewayConfig, GatewayRoute, GatewayState, GatewayTarget, RouteRequest, BasicAuthInput } from './schemas'

const selectCls =
  'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50'

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
  const caps = state.capabilities
  const selectedIsLocal = cfg.node_mac !== '' && cfg.node_mac.toLowerCase() === caps.local_mac.toLowerCase()

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
            <select className={selectCls} value={cfg.node_mac} onChange={(e) => update({ node_mac: e.target.value })}>
              <option value="">— pick a node —</option>
              {candidates.map((h) => (
                <option key={h.id} value={h.mac_address}>
                  {h.display_name || h.name}
                  {h.ipv4 ? ` (${h.ipv4})` : ''}
                  {h.mac_address.toLowerCase() === caps.local_mac.toLowerCase() ? ' · this node' : ''}
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
                  <div className="flex items-end gap-2 pb-1">
                    <Switch checked={!!cfg.acme_staging} onCheckedChange={(v) => update({ acme_staging: v })} />
                    <span className="text-sm">Staging CA (testing; untrusted certs, no rate limits)</span>
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
      </CardContent>
    </Card>
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
      {st.file_path && <code className="truncate">{st.file_path}</code>}
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
  basic_auth: [],
  ip_allow_list: '',
  enabled: true,
}

function targetKey(t: GatewayTarget) {
  return `${t.ipv4}:${t.port}`
}

function RouteForm({
  initial,
  routeId,
  onDone,
}: {
  initial: RouteRequest
  routeId?: string
  onDone: () => void
}) {
  const [req, setReq] = useState<RouteRequest>(initial)
  const [manual, setManual] = useState(!!initial.target_host && !initial.target_label)
  const { data: targets = [] } = useGatewayTargets()
  const create = useCreateRoute()
  const update = useUpdateRoute()
  const check = useCheckTarget()
  const pending = create.isPending || update.isPending

  const set = (patch: Partial<RouteRequest>) => setReq((r) => ({ ...r, ...patch }))

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
  const [reach, setReach] = useState<{ ok: boolean; error?: string; key: string } | null>(null)
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
    if (reachTimer.current) window.clearTimeout(reachTimer.current)
    reachTimer.current = window.setTimeout(() => {
      checkMutate(
        { host, port },
        {
          onSuccess: (r) => setReach({ ok: r.reachable, error: r.error, key }),
          onError: (e) => setReach({ ok: false, error: e.message, key }),
        }
      )
    }, 500)
    return () => {
      if (reachTimer.current) window.clearTimeout(reachTimer.current)
    }
  }, [req.target_host, req.target_port, checkMutate])
  const reachCurrent = reach && reach.key === `${req.target_host.trim()}:${Number(req.target_port)}` ? reach : null

  const setAuth = (i: number, patch: Partial<BasicAuthInput>) =>
    set({ basic_auth: req.basic_auth.map((a, j) => (j === i ? { ...a, ...patch } : a)) })

  return (
    <div className="space-y-4 rounded-lg border border-border/60 bg-muted/10 p-4">
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Domain</Label>
          <Input placeholder="grafana.example.com" value={req.domain} onChange={(e) => set({ domain: e.target.value })} />
        </div>
        <div className="space-y-1.5">
          <Label>Path prefix (optional)</Label>
          <Input placeholder="/" value={req.path_prefix} onChange={(e) => set({ path_prefix: e.target.value })} />
        </div>
      </div>

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
        ) : (
          <div className="grid gap-2 md:grid-cols-[110px_1fr_120px]">
            <select
              className={selectCls}
              value={req.target_scheme}
              onChange={(e) => set({ target_scheme: e.target.value })}
            >
              <option value="http">http</option>
              <option value="https">https</option>
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
        )}
        <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
          {req.target_host && req.target_port ? (
            <span className="font-mono">
              {req.target_scheme}://{req.target_host}:{req.target_port}
            </span>
          ) : (
            <span>Pick a detected service or enter an address reachable from the gateway node.</span>
          )}
          {req.target_host && req.target_port ? (
            <span className="inline-flex items-center gap-1.5" title={reachCurrent?.error}>
              <span
                className={cn(
                  'h-2 w-2 rounded-full',
                  !reachCurrent || check.isPending ? 'bg-zinc-400 animate-pulse' : reachCurrent.ok ? 'bg-emerald-400' : 'bg-red-400'
                )}
              />
              {!reachCurrent || check.isPending
                ? 'checking from this node…'
                : reachCurrent.ok
                  ? 'reachable from this node'
                  : `unreachable from this node${reachCurrent.error ? ` — ${reachCurrent.error}` : ''}`}
            </span>
          ) : null}
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
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Name (optional)</Label>
          <Input placeholder={req.domain || 'Grafana'} value={req.name} onChange={(e) => set({ name: e.target.value })} />
        </div>
        <div className="flex items-end gap-2 pb-1">
          <Switch checked={req.tls} onCheckedChange={(v) => set({ tls: v })} />
          <span className="text-sm">HTTPS (TLS terminated on the gateway; http redirects)</span>
        </div>
      </div>

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
      </div>

      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone} disabled={pending}>
          Cancel
        </Button>
        <Button type="button" size="sm" onClick={submit} disabled={pending || !req.domain || !req.target_host || !req.target_port}>
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
          <div className="flex flex-wrap items-center gap-2">
            <a
              href={route.public_url}
              target="_blank"
              rel="noreferrer"
              className={cn('truncate font-medium hover:underline', !route.enabled && 'text-muted-foreground line-through')}
            >
              {route.domain}
              {route.path_prefix}
            </a>
            {route.tls ? (
              <Badge variant="secondary" className="text-[10px] uppercase">https</Badge>
            ) : (
              <Badge variant="outline" className="text-[10px] uppercase">http</Badge>
            )}
            {route.protected ? (
              <span className="inline-flex items-center gap-1 text-xs text-emerald-500" title="basic auth / IP allow list">
                <ShieldCheck className="h-3.5 w-3.5" /> protected
              </span>
            ) : (
              <span className="inline-flex items-center gap-1 text-xs text-amber-500" title="no basic auth or IP allow list">
                <ShieldAlert className="h-3.5 w-3.5" /> public
              </span>
            )}
          </div>
          <div className="truncate font-mono text-xs text-muted-foreground">
            → {route.target_url || `${route.target_scheme}://${route.target_host}:${route.target_port}`}
            {route.rewritten ? (
              <span
                className="font-sans text-amber-500"
                title="This target is on the gateway host itself; from inside the Traefik container it is reached via host.docker.internal. The stored address is kept so the route still works if the gateway moves."
              >
                {' '}
                · Traefik uses <span className="font-mono">{route.effective_url}</span>
              </span>
            ) : null}
            {route.target_label ? <span className="font-sans"> · {route.target_label}</span> : null}
            {route.name && route.name !== route.domain ? <span className="font-sans"> · {route.name}</span> : null}
          </div>
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
  const [tail, setTail] = useState(300)
  const enabled = open && state.status.is_gateway_node && state.config.mode === 'managed'
  const { data, isFetching, refetch } = useGatewayLogs(enabled, tail)
  const endRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'end' })
  }, [data?.logs])

  if (!state.config.enabled || !state.status.is_gateway_node || state.config.mode !== 'managed') return null

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <ScrollText className="h-4 w-4" /> Traefik logs
            </CardTitle>
            <CardDescription>
              Service log + access log of the managed Traefik on this node
              {state.capabilities.manage_kind === 'systemd' ? ' (journalctl)' : ' (docker logs)'}. Refreshes every 5 s.
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            {open && (
              <>
                <select className={cn(selectCls, 'h-8 w-auto')} value={tail} onChange={(e) => setTail(Number(e.target.value))}>
                  {[100, 300, 1000].map((n) => (
                    <option key={n} value={n}>
                      last {n}
                    </option>
                  ))}
                </select>
                <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => refetch()} title="Refresh">
                  <RefreshCw className={cn('h-4 w-4', isFetching && 'animate-spin')} />
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
          <pre className="max-h-96 overflow-auto rounded-lg border border-border/60 bg-black/80 p-3 font-mono text-[11px] leading-snug text-zinc-200 whitespace-pre-wrap break-all">
            {data?.logs?.trim() ? data.logs : isFetching ? 'loading…' : 'no output yet'}
            <div ref={endRef} />
          </pre>
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
              <RouteForm initial={emptyRequest} onDone={() => setAdding(false)} />
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
                    <RouteForm initial={routeToRequest(r)} routeId={r.route_id} onDone={() => setEditing(null)} />
                  </div>
                ) : (
                  <RouteRow key={r.route_id} route={r} onEdit={() => { setEditing(r.route_id); setAdding(false) }} />
                )
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <LogsCard state={data} />
    </div>
  )
}
