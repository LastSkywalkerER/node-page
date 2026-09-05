import { useState } from 'react'
import { format } from 'date-fns'
import { toast } from 'sonner'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { Plug, RefreshCw, Trash2, ShieldAlert, Image as ImageIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/shared/ui/switch'
import { OSIcon } from '@/shared/components/OSIcon'
import { cn } from '@/lib/utils'
import { useConnectors, useTestProxmox, useCreateProxmox, useTestPBS, useCreatePBS, useSavePexels, useToggleConnector, useDeleteConnector, useSyncConnector } from './useConnectors'
import { useQueryClient } from '@tanstack/react-query'
import type { Connector, ConnectorPreview, DiscoveredHint } from './schemas'
import { BackupRepoCard } from './BackupRepoCard'

function fmtSyncTime(iso: string): string {
  if (!iso) return 'never'
  const d = new Date(iso)
  if (isNaN(d.getTime()) || d.getTime() <= 0) return 'never'
  return format(d, 'HH:mm:ss dd.MM.yyyy')
}

const statusStyles: Record<string, { dot: string; label: string }> = {
  ok: { dot: 'bg-emerald-400', label: 'OK' },
  pending: { dot: 'bg-amber-400', label: 'Pending first sync' },
  unreachable: { dot: 'bg-red-400', label: 'Unreachable' },
  auth_failed: { dot: 'bg-red-400', label: 'Auth failed' },
  disabled: { dot: 'bg-zinc-400', label: 'Disabled' },
}

function StatusBadge({ status }: { status: string }) {
  const s = statusStyles[status] ?? { dot: 'bg-zinc-400', label: status || 'unknown' }
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className={cn('h-2 w-2 rounded-full', s.dot)} />
      {s.label}
    </span>
  )
}

function HintCard({ hint, onConnect }: { hint: DiscoveredHint; onConnect: (hint: DiscoveredHint) => void }) {
  return (
    <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 dark:border-amber-400/25">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-3">
          <OSIcon host={{ platform: 'proxmox' }} className="mt-0.5 h-6 w-6 text-amber-500 dark:text-amber-400" />
          <div className="min-w-0">
            <div className="font-display text-sm font-semibold tracking-wide">
              This machine looks like a Proxmox {hint.guest_kind === 'lxc' ? 'LXC container' : 'virtual machine'}
              {hint.vmid_hint ? <span className="font-mono text-muted-foreground"> · VMID {hint.vmid_hint}</span> : null}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              Connect the Proxmox API to see the hypervisor and all of its VMs/LXCs as machines — without installing
              an agent on each one.
            </p>
            <div className="mt-2 flex flex-wrap items-center gap-1.5">
              <Badge variant="secondary" className="border border-border/60 text-[10px] uppercase">
                confidence: {hint.confidence}
              </Badge>
              {hint.evidence.map((e) => (
                <span key={e} className="rounded bg-muted/60 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                  {e}
                </span>
              ))}
            </div>
          </div>
        </div>
        <Button size="sm" className="shrink-0 gap-1.5" onClick={() => onConnect(hint)}>
          <Plug className="h-3.5 w-3.5" /> Connect
        </Button>
      </div>
    </div>
  )
}

type ConnectKind = 'proxmox' | 'pbs'

// Per-kind copy/config for the shared connect form. Proxmox VE and Proxmox
// Backup Server share the same token-auth flow; only ports, names, the default
// role and the preview line differ.
const KIND_COPY: Record<ConnectKind, {
  product: string
  port: string
  endpointPlaceholder: string
  tokenPlaceholder: string
  role: string
  permPath: string
  successToast: string
}> = {
  proxmox: {
    product: 'Proxmox',
    port: '8006',
    endpointPlaceholder: 'https://192.168.1.2:8006',
    tokenPlaceholder: 'root@pam!node-stats',
    role: 'PVEAuditor',
    permPath: 'Datacenter → Permissions',
    successToast: 'Proxmox connected — discovering machines…',
  },
  pbs: {
    product: 'Proxmox Backup Server',
    port: '8007',
    endpointPlaceholder: 'https://192.168.1.3:8007',
    tokenPlaceholder: 'monitor@pbs!node-stats',
    role: 'Audit',
    permPath: 'Configuration → Access Control → Permissions',
    successToast: 'Proxmox Backup Server connected — fetching status…',
  },
}

function ConnectForm({ kind = 'proxmox', suggestedEndpoint, onDone }: { kind?: ConnectKind; suggestedEndpoint?: string; onDone: () => void }) {
  const copy = KIND_COPY[kind]
  const [endpoint, setEndpoint] = useState(suggestedEndpoint ?? 'https://')
  const [tokenId, setTokenId] = useState('')
  const [secret, setSecret] = useState('')
  const [skipVerify, setSkipVerify] = useState(true)
  const [preview, setPreview] = useState<ConnectorPreview | null>(null)
  // Errors render inline (and survive) — privilege-separation failures carry
  // multi-line fix instructions a toast would eat.
  const [errMsg, setErrMsg] = useState<string | null>(null)

  // Both kinds' hooks are created unconditionally (rules of hooks); we pick by kind.
  const testPve = useTestProxmox()
  const createPve = useCreateProxmox()
  const testPbs = useTestPBS()
  const createPbs = useCreatePBS()
  const test = kind === 'pbs' ? testPbs : testPve
  const create = kind === 'pbs' ? createPbs : createPve

  const req = { endpoint, token_id: tokenId, secret, skip_tls_verify: skipVerify }
  const canSubmit = endpoint.trim() !== '' && tokenId.trim() !== '' && secret.trim() !== ''

  const onTest = () =>
    test.mutate(req, {
      onSuccess: (p) => {
        setPreview(p)
        setErrMsg(null)
      },
      onError: (e) => {
        setPreview(null)
        setErrMsg(e.message)
        toast.error('Connection test failed')
      },
    })

  const onSave = () =>
    create.mutate(req, {
      onSuccess: () => {
        toast.success(copy.successToast)
        onDone()
      },
      onError: (e) => {
        setErrMsg(e.message)
        toast.error('Connect failed')
      },
    })

  return (
    <div className="space-y-3 rounded-lg border border-border/60 p-4 dark:border-white/10">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="conn-endpoint">API endpoint</Label>
          <Input
            id="conn-endpoint"
            placeholder={copy.endpointPlaceholder}
            value={endpoint}
            onChange={(e) => setEndpoint(e.target.value)}
          />
          <p className="text-[11px] text-muted-foreground">
            The address you open the {copy.product} web UI on — same host, port <span className="font-mono">{copy.port}</span>.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="conn-token-id">API token id</Label>
          <Input
            id="conn-token-id"
            placeholder={copy.tokenPlaceholder}
            value={tokenId}
            onChange={(e) => setTokenId(e.target.value)}
          />
          <p className="text-[11px] text-muted-foreground">
            Format <span className="font-mono">user@realm!token-name</span> — copy it from the token list.
          </p>
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="conn-secret">Token secret</Label>
        <Input
          id="conn-secret"
          type="password"
          placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">
          The UUID {copy.product} shows <em>once</em> when the token is created. Lost it? Just create a new token.
        </p>
      </div>
      <div className="rounded-md border border-border/50 bg-muted/15 px-3 py-2 text-[11px] leading-relaxed text-muted-foreground dark:border-white/8">
        <span className="font-medium text-foreground">No token yet? Two steps in the {copy.product} UI</span> (read-only access):
        <ol className="ml-4 mt-1 list-decimal space-y-0.5">
          <li>
            {copy.permPath} → <span className="font-mono">API Tokens</span> → Add → copy the secret.
          </li>
          <li>
            {copy.permPath} → Add → <span className="font-mono">API Token Permission</span>: path <span className="font-mono">/</span>,
            your token, role <span className="font-mono">{copy.role}</span>.
            Without this the token authenticates but sees nothing.
          </li>
        </ol>
      </div>
      <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
        <Switch checked={skipVerify} onCheckedChange={setSkipVerify} />
        Skip TLS certificate verification
        <ShieldAlert className="h-3.5 w-3.5 text-amber-400" />
        <span className="text-[11px]">({copy.product} ships a self-signed certificate by default)</span>
      </label>

      {errMsg && (
        <div className="whitespace-pre-wrap break-words rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 font-mono text-xs text-red-400">
          {errMsg}
        </div>
      )}

      {preview && (
        <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-xs">
          <span className="font-medium text-emerald-500 dark:text-emerald-400">Reachable.</span>{' '}
          <span className="font-mono">{preview.cluster_name}</span> · {copy.product} {preview.version} · {preview.nodes.length}{' '}
          node{preview.nodes.length === 1 ? '' : 's'}
          {kind === 'pbs' ? (
            <> · {preview.guest_count} datastore{preview.guest_count === 1 ? '' : 's'}</>
          ) : (
            <> · {preview.guest_count} guest{preview.guest_count === 1 ? '' : 's'}</>
          )}
          {preview.matched_hosts > 0 && (
            <> · <span className="text-emerald-500 dark:text-emerald-400">{preview.matched_hosts} already registered here (will be linked, not duplicated)</span></>
          )}
        </div>
      )}

      <div className="flex items-center gap-2">
        <Button variant="outline" size="sm" disabled={!canSubmit || test.isPending} onClick={onTest}>
          {test.isPending ? 'Testing…' : 'Test connection'}
        </Button>
        <Button size="sm" disabled={!canSubmit || create.isPending} onClick={onSave}>
          {create.isPending ? 'Connecting…' : 'Connect'}
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function ConnectorRow({ connector }: { connector: Connector }) {
  const toggle = useToggleConnector()
  const del = useDeleteConnector()
  const sync = useSyncConnector()

  const onDelete = async () => {
    const { confirmed, checked: removeHosts } = await confirmDialog({
      title: 'Remove connector?',
      description: `Remove the ${connector.type} connector "${connector.fingerprint}"?`,
      variant: 'destructive',
      confirmText: 'Remove',
      checkbox: {
        label:
          'Also remove the machines it discovered (hypervisor + agent-less guests) and their metrics. ' +
          'Machines running a node-stats agent are only unlinked and stay either way.',
      },
    })
    if (!confirmed) return
    del.mutate(
      { id: connector.id, removeHosts },
      {
        onSuccess: () => toast.success('Connector removed'),
        onError: (e) => toast.error('Remove failed: ' + e.message),
      }
    )
  }

  return (
    <div className="px-4 py-3 transition-colors hover:bg-muted/20">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <OSIcon host={{ platform: connector.type }} className="h-5 w-5 text-muted-foreground" />
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="truncate font-medium">{connector.fingerprint}</span>
              <StatusBadge status={connector.enabled ? connector.status : 'disabled'} />
            </div>
            <div className="truncate font-mono text-xs text-muted-foreground">
              {connector.endpoint} · {connector.token_id} · last sync {fmtSyncTime(connector.last_sync_at)}
            </div>
            {connector.last_error && connector.status !== 'ok' && (
              <div className="mt-0.5 truncate text-xs text-red-400" title={connector.last_error}>
                {connector.last_error}
              </div>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Switch
            checked={connector.enabled}
            disabled={toggle.isPending}
            onCheckedChange={(enabled) =>
              toggle.mutate(
                { id: connector.id, enabled },
                { onError: (e) => toast.error('Toggle failed: ' + e.message) }
              )
            }
          />
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 text-muted-foreground/60 hover:text-foreground"
            disabled={sync.isPending}
            onClick={() => sync.mutate(connector.id, { onSuccess: () => toast.info('Sync requested') })}
            title="Sync now"
          >
            <RefreshCw className={cn('h-3.5 w-3.5', sync.isPending && 'animate-spin')} />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 text-muted-foreground/60 hover:bg-destructive/10 hover:text-destructive"
            disabled={del.isPending}
            onClick={onDelete}
            title="Remove connector"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </div>
  )
}

const WALLPAPER_PRESETS = ['cyberpunk city', 'abstract dark', 'deep space', 'server room', 'mountains night', 'minimal architecture']

function parsePexelsQuery(config: string): string {
  try {
    const q = (JSON.parse(config) as { query?: string })?.query
    return typeof q === 'string' ? q : ''
  } catch {
    return ''
  }
}

/**
 * Dynamic wallpaper connector (Pexels). The API key lives encrypted on the
 * backend; browsers get photos through GET /wallpaper, rotating every 5 min
 * and following the dark/light theme (dark mode requests black-toned photos).
 */
function WallpaperCard({ connector }: { connector?: Connector }) {
  const save = useSavePexels()
  const toggle = useToggleConnector()
  const del = useDeleteConnector()
  const queryClient = useQueryClient()
  const [apiKey, setApiKey] = useState('')
  // null = untouched → mirror the stored query once the connector loads.
  const [queryDraft, setQueryDraft] = useState<string | null>(null)
  const query = queryDraft ?? (connector ? parsePexelsQuery(connector.config) : 'cyberpunk city')

  const onSave = () =>
    save.mutate(
      { api_key: apiKey.trim(), query: query.trim() },
      {
        onSuccess: () => {
          setApiKey('')
          toast.success('Wallpaper connected — the background refreshes within a few seconds')
        },
        onError: (e) => toast.error('Save failed: ' + e.message),
      }
    )

  const refreshWallpaper = () => queryClient.invalidateQueries({ queryKey: ['wallpaper'] })

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 font-display text-lg tracking-wide">
          <ImageIcon className="h-4 w-4 text-muted-foreground" /> Wallpaper
        </CardTitle>
        <CardDescription>
          Rotate the dashboard background every 5 minutes with fresh 4K photos from Pexels. Without
          a key the bundled art is used. Dark theme automatically asks for dark-toned photos.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 pt-2">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="pexels-key">Pexels API key</Label>
            <Input
              id="pexels-key"
              type="password"
              placeholder={connector ? '•••••• (stored — leave empty to keep)' : '563492ad6f917…'}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">
              Free at{' '}
              <a href="https://www.pexels.com/api/" target="_blank" rel="noreferrer" className="underline hover:text-foreground">
                pexels.com/api
              </a>{' '}
              — sign up, the key is shown instantly. It stays on the server, encrypted.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="pexels-query">Theme</Label>
            <Input
              id="pexels-query"
              placeholder="cyberpunk city"
              value={query}
              onChange={(e) => setQueryDraft(e.target.value)}
            />
            <div className="flex flex-wrap gap-1">
              {WALLPAPER_PRESETS.map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => setQueryDraft(p)}
                  className={cn(
                    'rounded border border-border/60 px-1.5 py-0.5 font-mono text-[10px] transition-colors',
                    p === query ? 'bg-primary/15 text-primary border-primary/40' : 'text-muted-foreground hover:text-foreground'
                  )}
                >
                  {p}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <Button size="sm" disabled={save.isPending || (!connector && apiKey.trim() === '')} onClick={onSave}>
            {save.isPending ? 'Checking key…' : connector ? 'Save' : 'Connect'}
          </Button>
          {connector && (
            <>
              <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
                <Switch
                  checked={connector.enabled}
                  disabled={toggle.isPending}
                  onCheckedChange={(enabled) =>
                    toggle.mutate(
                      { id: connector.id, enabled },
                      { onSuccess: refreshWallpaper, onError: (e) => toast.error('Toggle failed: ' + e.message) }
                    )
                  }
                />
                {connector.enabled ? 'On' : 'Off'}
              </label>
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7 text-muted-foreground/60 hover:bg-destructive/10 hover:text-destructive"
                disabled={del.isPending}
                onClick={async () => {
                  const { confirmed } = await confirmDialog({
                    title: 'Remove wallpaper connector?',
                    description: 'The bundled background returns.',
                    variant: 'destructive',
                    confirmText: 'Remove',
                  })
                  if (!confirmed) return
                  del.mutate(
                    { id: connector.id, removeHosts: false },
                    { onSuccess: () => { refreshWallpaper(); toast.success('Wallpaper connector removed') } }
                  )
                }}
                title="Remove wallpaper connector"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

export function ConnectorsTab() {
  const { data, isLoading } = useConnectors()
  const [formHint, setFormHint] = useState<DiscoveredHint | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [formKind, setFormKind] = useState<ConnectKind>('proxmox')

  const discovered = data?.discovered ?? []
  // This registry also stores rows that are NOT machine-list data sources —
  // the wallpaper key and the application backup repository — each of which has
  // its own card below. A repository has nothing to "sync", so listing it here
  // beside Proxmox would only mislead.
  const configured = (data?.configured ?? []).filter(
    (c) => c.type !== 'pexels' && c.type !== 'appbackup'
  )
  const pexels = (data?.configured ?? []).find((c) => c.type === 'pexels')

  return (
    <div className="space-y-4">
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Connectors</CardTitle>
        <CardDescription>
          External data sources that enrich the machine list — e.g. a Proxmox hypervisor whose VMs and LXCs appear
          nested under their node, linked to agents by MAC address so nothing is duplicated.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 pt-2">
        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : (
          <>
            {discovered.map((hint) => (
              <HintCard
                key={hint.type}
                hint={hint}
                onConnect={(h) => {
                  setFormHint(h)
                  setFormKind('proxmox')
                  setFormOpen(true)
                }}
              />
            ))}

            {formOpen ? (
              <ConnectForm
                kind={formKind}
                suggestedEndpoint={formHint?.suggested_endpoint || undefined}
                onDone={() => {
                  setFormOpen(false)
                  setFormHint(null)
                }}
              />
            ) : (
              // Always available: several independent Proxmox / PBS servers can
              // be added side by side; each becomes its own entry below.
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" className="gap-1.5" onClick={() => { setFormHint(null); setFormKind('proxmox'); setFormOpen(true) }}>
                  <Plug className="h-3.5 w-3.5" /> Add Proxmox connector
                </Button>
                <Button variant="outline" size="sm" className="gap-1.5" onClick={() => { setFormHint(null); setFormKind('pbs'); setFormOpen(true) }}>
                  <Plug className="h-3.5 w-3.5" /> Add Backup Server connector
                </Button>
              </div>
            )}

            {configured.length > 0 && (
              <div className="rounded-lg border border-border/60 dark:border-white/10">
                <div className="divide-y divide-border/60 dark:divide-white/10">
                  {configured.map((c) => (
                    <ConnectorRow key={c.id} connector={c} />
                  ))}
                </div>
              </div>
            )}

            {configured.length === 0 && discovered.length === 0 && !formOpen && (
              <p className="text-xs text-muted-foreground">
                No hypervisor environment detected on this machine. You can still connect a Proxmox API manually.
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>

    <BackupRepoCard />

    <WallpaperCard connector={pexels} />
    </div>
  )
}
