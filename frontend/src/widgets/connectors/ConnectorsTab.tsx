import { useState } from 'react'
import { format } from 'date-fns'
import { toast } from 'sonner'
import { Plug, RefreshCw, Trash2, ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/shared/ui/switch'
import { OSIcon } from '@/shared/components/OSIcon'
import { cn } from '@/lib/utils'
import { useConnectors, useTestProxmox, useCreateProxmox, useToggleConnector, useDeleteConnector, useSyncConnector } from './useConnectors'
import type { Connector, ConnectorPreview, DiscoveredHint } from './schemas'

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

function ConnectForm({ suggestedEndpoint, onDone }: { suggestedEndpoint?: string; onDone: () => void }) {
  const [endpoint, setEndpoint] = useState(suggestedEndpoint ?? 'https://')
  const [tokenId, setTokenId] = useState('')
  const [secret, setSecret] = useState('')
  const [skipVerify, setSkipVerify] = useState(true)
  const [preview, setPreview] = useState<ConnectorPreview | null>(null)

  const test = useTestProxmox()
  const create = useCreateProxmox()

  const req = { endpoint, token_id: tokenId, secret, skip_tls_verify: skipVerify }
  const canSubmit = endpoint.trim() !== '' && tokenId.trim() !== '' && secret.trim() !== ''

  const onTest = () =>
    test.mutate(req, {
      onSuccess: (p) => setPreview(p),
      onError: (e) => {
        setPreview(null)
        toast.error('Connection test failed: ' + e.message)
      },
    })

  const onSave = () =>
    create.mutate(req, {
      onSuccess: () => {
        toast.success('Proxmox connected — discovering machines…')
        onDone()
      },
      onError: (e) => toast.error('Connect failed: ' + e.message),
    })

  return (
    <div className="space-y-3 rounded-lg border border-border/60 p-4 dark:border-white/10">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="pve-endpoint">API endpoint</Label>
          <Input
            id="pve-endpoint"
            placeholder="https://192.168.1.2:8006"
            value={endpoint}
            onChange={(e) => setEndpoint(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="pve-token-id">API token id</Label>
          <Input
            id="pve-token-id"
            placeholder="user@pam!node-stats"
            value={tokenId}
            onChange={(e) => setTokenId(e.target.value)}
          />
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="pve-secret">Token secret</Label>
        <Input
          id="pve-secret"
          type="password"
          placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">
          Create a read-only token in Proxmox: Datacenter → Permissions → API Tokens, role <span className="font-mono">PVEAuditor</span> on{' '}
          <span className="font-mono">/</span> (uncheck “Privilege Separation” or grant the role to the token itself).
        </p>
      </div>
      <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
        <Switch checked={skipVerify} onCheckedChange={setSkipVerify} />
        Skip TLS certificate verification
        <ShieldAlert className="h-3.5 w-3.5 text-amber-400" />
        <span className="text-[11px]">(Proxmox ships a self-signed certificate by default)</span>
      </label>

      {preview && (
        <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-xs">
          <span className="font-medium text-emerald-500 dark:text-emerald-400">Reachable.</span>{' '}
          <span className="font-mono">{preview.cluster_name}</span> · PVE {preview.version} · {preview.nodes.length}{' '}
          node{preview.nodes.length === 1 ? '' : 's'} · {preview.guest_count} guest{preview.guest_count === 1 ? '' : 's'}
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

  const onDelete = () => {
    if (!window.confirm(`Remove the ${connector.type} connector "${connector.fingerprint}"?`)) return
    const removeHosts = window.confirm(
      'Also remove the machines it discovered (hypervisor + agent-less guests) and their metrics?\n\n' +
        'Machines running a node-stats agent are only unlinked and stay either way.'
    )
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

export function ConnectorsTab() {
  const { data, isLoading } = useConnectors()
  const [formHint, setFormHint] = useState<DiscoveredHint | null>(null)
  const [formOpen, setFormOpen] = useState(false)

  const discovered = data?.discovered ?? []
  const configured = data?.configured ?? []

  return (
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
                  setFormOpen(true)
                }}
              />
            ))}

            {formOpen ? (
              <ConnectForm
                suggestedEndpoint={formHint?.suggested_endpoint || undefined}
                onDone={() => {
                  setFormOpen(false)
                  setFormHint(null)
                }}
              />
            ) : (
              discovered.length === 0 && (
                <Button variant="outline" size="sm" className="gap-1.5" onClick={() => setFormOpen(true)}>
                  <Plug className="h-3.5 w-3.5" /> Connect Proxmox manually
                </Button>
              )
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
  )
}
