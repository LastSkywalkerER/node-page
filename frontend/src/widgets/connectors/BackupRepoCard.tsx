import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Archive, HardDrive, Loader2, ServerCog, TriangleAlert } from 'lucide-react'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { apiClient } from '@/shared/lib/api'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { useAppBackupStatus, useRepoStats } from '@/widgets/applications/useAppBackup'

type Backend = 'local' | 'sftp' | 's3'

interface RepoForm {
  backend: Backend
  password: string
  /** Create the repository WITHOUT encryption — an explicit choice, never a blank field. */
  no_password: boolean
  // local
  path: string
  // sftp
  host: string
  port: string
  user: string
  remote_path: string
  ssh_private_key: string
  // s3
  endpoint: string
  bucket: string
  prefix: string
  region: string
  access_key_id: string
  s3_secret_key: string
}

const emptyForm: RepoForm = {
  backend: 'sftp',
  password: '',
  no_password: false,
  path: '',
  host: '',
  port: '',
  user: '',
  remote_path: '',
  ssh_private_key: '',
  endpoint: '',
  bucket: '',
  prefix: '',
  region: '',
  access_key_id: '',
  s3_secret_key: '',
}

function fmtBytes(b?: number): string {
  if (!b) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = b
  let i = 0
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`
}

function apiError(e: unknown): string {
  const err = e as { response?: { data?: { error?: string } }; message?: string }
  return err?.response?.data?.error ?? err?.message ?? 'Request failed'
}

/** Turns the form into the API payload, dropping fields the backend ignores. */
function payload(f: RepoForm): Record<string, unknown> {
  const base: Record<string, unknown> = {
    backend: f.backend,
    password: f.no_password ? '' : f.password,
    no_password: f.no_password,
  }
  if (f.backend === 'local') return { ...base, path: f.path.trim() }
  if (f.backend === 'sftp')
    return {
      ...base,
      host: f.host.trim(),
      port: f.port ? Number(f.port) : 0,
      user: f.user.trim(),
      remote_path: f.remote_path.trim(),
      ssh_private_key: f.ssh_private_key,
    }
  return {
    ...base,
    endpoint: f.endpoint.trim(),
    bucket: f.bucket.trim(),
    prefix: f.prefix.trim(),
    region: f.region.trim(),
    access_key_id: f.access_key_id.trim(),
    s3_secret_key: f.s3_secret_key,
  }
}

const BACKENDS: { key: Backend; label: string; hint: string }[] = [
  { key: 'sftp', label: 'SSH / SFTP', hint: 'Any SSH host — this is also how a Synology NAS is used as a target.' },
  { key: 's3', label: 'S3', hint: 'Any S3-compatible object store: Backblaze B2, Wasabi, MinIO, AWS.' },
  {
    key: 'local',
    label: 'Filesystem',
    hint: 'A directory on this machine — node-stats mounts it into itself. Node-local: it protects only this node, and it dies with the machine it protects.',
  },
]

/**
 * The repository application backups are written to — one per cluster.
 *
 * It lives in the same registry as the connectors above (that is what gives it
 * Raft replication and an encrypted secret), but it is not a data source: it
 * enriches no machine and has nothing to sync, so it gets its own card rather
 * than a row in a list about hypervisors.
 */
export function BackupRepoCard() {
  const { data: status, isLoading } = useAppBackupStatus()
  const { data: stats } = useRepoStats(!!status?.repo_configured)
  const qc = useQueryClient()
  const [form, setForm] = useState<RepoForm>(emptyForm)
  const [editing, setEditing] = useState(false)

  // Prefill from the stored configuration; the password is never returned, so
  // changing anything means retyping it.
  useEffect(() => {
    const r = status?.repo as Record<string, unknown> | undefined
    if (!r || editing) return
    setForm((f) => ({
      ...f,
      backend: (r.backend as Backend) ?? f.backend,
      path: (r.path as string) ?? '',
      host: (r.host as string) ?? '',
      port: r.port ? String(r.port) : '',
      user: (r.user as string) ?? '',
      remote_path: (r.remote_path as string) ?? '',
      endpoint: (r.endpoint as string) ?? '',
      bucket: (r.bucket as string) ?? '',
      prefix: (r.prefix as string) ?? '',
      region: (r.region as string) ?? '',
      access_key_id: (r.access_key_id as string) ?? '',
      no_password: !!r.no_password,
    }))
  }, [status?.repo, editing])

  // Offer a directory beside the installation rather than an empty field: the
  // operator names a place on the machine, node-stats arranges the plumbing.
  useEffect(() => {
    if (editing || form.path || !status?.suggested_path) return
    setForm((f) => ({ ...f, path: status.suggested_path as string }))
  }, [status?.suggested_path, editing, form.path])

  const set = (patch: Partial<RepoForm>) => {
    setEditing(true)
    setForm((f) => ({ ...f, ...patch }))
  }

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['app-backup-status'] })
    qc.invalidateQueries({ queryKey: ['connectors'] })
  }

  const test = useMutation({
    mutationFn: async () => (await apiClient.post('/app-backup/repo/test', payload(form))).data,
  })
  const save = useMutation({
    mutationFn: async () => (await apiClient.put('/app-backup/repo', payload(form))).data,
    onSuccess: () => {
      setEditing(false)
      invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: async () => {
      await apiClient.delete('/app-backup/repo')
    },
    onSuccess: invalidate,
  })

  async function onSave() {
    if (form.backend === 'local') {
      const { confirmed } = await confirmDialog({
        title: 'Store backups on this node?',
        description: (
          <span>
            A repository on the same machine it protects is lost with that machine — a failed disk, a
            wiped host or a ransomware run takes the backups with it. Use it only alongside a second,
            off-machine repository.
            {!status?.self_mount_required && (
              <>
                <br />
                <br />
                <strong>node-stats will restart:</strong> the directory has to be mounted into the
                container, so the controller recreates it. The dashboard comes back in a few seconds.
              </>
            )}
          </span>
        ),
        variant: 'destructive',
        confirmText: 'I understand, save',
      })
      if (!confirmed) return
    }
    save.mutate(undefined, {
      onSuccess: () => toast.success('Repository saved'),
      onError: (e) => toast.error(apiError(e)),
    })
  }

  async function onRemove() {
    const { confirmed } = await confirmDialog({
      title: 'Remove the backup repository?',
      description:
        'Only the configuration is removed — the snapshots stay where they are. Applications will not be backed up until a repository is configured again.',
      variant: 'destructive',
      confirmText: 'Remove',
    })
    if (!confirmed) return
    remove.mutate(undefined, {
      onSuccess: () => toast.success('Repository removed'),
      onError: (e) => toast.error(apiError(e)),
    })
  }

  if (isLoading) return null
  const configured = !!status?.repo_configured
  const backendHint = BACKENDS.find((b) => b.key === form.backend)?.hint

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center gap-2">
          <Archive className="h-4 w-4 text-muted-foreground" />
          <span className="text-base font-semibold">Backup repository</span>
          {configured && (
            <Badge variant="outline" className="border-emerald-500/40 bg-emerald-500/15 text-[10px] text-emerald-600">
              configured
            </Badge>
          )}
          {configured && status?.scope && (
            <Badge variant="outline" className="text-[10px]">
              {status.scope === 'node' ? 'this node only' : 'whole cluster'}
            </Badge>
          )}
          {status?.mount_pending && (
            <Badge variant="outline" className="border-amber-500/40 bg-amber-500/15 text-[10px] text-amber-600">
              mount pending
            </Badge>
          )}
          {status?.restic_installed && (
            <Badge variant="outline" className="text-[10px]">
              restic {status.restic_version}
            </Badge>
          )}
          {!status?.controller_ready && (
            <Badge variant="outline" className="border-amber-500/40 bg-amber-500/15 text-[10px] text-amber-600">
              no controller
            </Badge>
          )}
        </div>
        <p className="text-sm text-muted-foreground">
          Where application backups are written. Snapshots are tagged per node, so every machine deduplicates against the
          others. Encrypted by restic unless you choose otherwise.
        </p>
      </CardHeader>

      <CardContent className="space-y-4">
        {configured && status?.repo && (
          <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/40 px-3 py-2 text-xs">
            <HardDrive className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate font-mono">{String(status.repo.url)}</span>
            {stats && (
              <span
                className="shrink-0 text-muted-foreground"
                title={
                  stats.compression_ratio > 1
                    ? `${fmtBytes(stats.uncompressed_size)} of data stored in ${fmtBytes(stats.total_size)} (${stats.compression_ratio.toFixed(1)}x compression, before deduplication)`
                    : undefined
                }
              >
                · <span className="font-medium text-foreground">{fmtBytes(stats.total_size)}</span> in{' '}
                {stats.snapshot_count} snapshot{stats.snapshot_count === 1 ? '' : 's'}
              </span>
            )}
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto h-7 shrink-0 px-2 text-destructive"
              onClick={onRemove}
              disabled={remove.isPending}
            >
              Remove
            </Button>
          </div>
        )}

        {!status?.controller_ready && (
          <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
            <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
            <span>
              No controller sidecar is running on this node. A repository can still be saved, but backups cannot run
              until one is present — only the controller may stop containers and write to host paths.
            </span>
          </div>
        )}

        <div className="flex flex-wrap gap-1">
          {BACKENDS.map((b) => (
            <button
              key={b.key}
              type="button"
              onClick={() => set({ backend: b.key })}
              className={cn(
                'cursor-pointer rounded-md border px-3 py-1 text-xs font-medium transition-colors',
                form.backend === b.key
                  ? 'border-primary bg-primary/10 text-foreground'
                  : 'border-border/60 text-muted-foreground hover:text-foreground'
              )}
            >
              {b.label}
            </button>
          ))}
        </div>
        {backendHint && <p className="text-xs text-muted-foreground">{backendHint}</p>}

        {form.backend === 'local' && (
          <div className="space-y-1">
            <Label htmlFor="repo-path">Directory on the host</Label>
            <Input
              id="repo-path"
              value={form.path}
              onChange={(e) => set({ path: e.target.value })}
              placeholder={status?.suggested_path ?? '/mnt/backup/restic'}
              className="font-mono"
            />
            {status?.self_mount_required ? (
              <p className="text-xs text-amber-600">{status.mount_hint}</p>
            ) : (
              <p className="text-xs text-muted-foreground">
                A path on the machine, not inside the container — node-stats mounts it into itself,
                which recreates the container on save.
              </p>
            )}
            <p className="text-xs text-destructive">
              A repository here dies with the machine it protects. Pair it with an off-machine one.
            </p>
          </div>
        )}

        {form.backend === 'sftp' && (
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="repo-host">Host</Label>
              <Input id="repo-host" value={form.host} onChange={(e) => set({ host: e.target.value })} placeholder="nas.local" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-port">Port</Label>
              <Input id="repo-port" value={form.port} onChange={(e) => set({ port: e.target.value })} placeholder="22" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-user">User</Label>
              <Input id="repo-user" value={form.user} onChange={(e) => set({ user: e.target.value })} placeholder="backup" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-remote">Remote path</Label>
              <Input
                id="repo-remote"
                value={form.remote_path}
                onChange={(e) => set({ remote_path: e.target.value })}
                placeholder="/volume1/restic"
                className="font-mono"
              />
            </div>
            <div className="space-y-1 sm:col-span-2">
              <Label htmlFor="repo-key">SSH private key</Label>
              <textarea
                id="repo-key"
                value={form.ssh_private_key}
                onChange={(e) => set({ ssh_private_key: e.target.value })}
                rows={3}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                className="w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs"
              />
              <p className="text-xs text-muted-foreground">Stored encrypted; password auth is not used.</p>
            </div>
          </div>
        )}

        {form.backend === 's3' && (
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="repo-endpoint">Endpoint</Label>
              <Input
                id="repo-endpoint"
                value={form.endpoint}
                onChange={(e) => set({ endpoint: e.target.value })}
                placeholder="s3.eu-central-003.backblazeb2.com"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-bucket">Bucket</Label>
              <Input id="repo-bucket" value={form.bucket} onChange={(e) => set({ bucket: e.target.value })} placeholder="node-stats-backups" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-prefix">Prefix</Label>
              <Input id="repo-prefix" value={form.prefix} onChange={(e) => set({ prefix: e.target.value })} placeholder="(optional)" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-region">Region</Label>
              <Input id="repo-region" value={form.region} onChange={(e) => set({ region: e.target.value })} placeholder="(optional)" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-akid">Access key ID</Label>
              <Input id="repo-akid" value={form.access_key_id} onChange={(e) => set({ access_key_id: e.target.value })} />
            </div>
            <div className="space-y-1">
              <Label htmlFor="repo-secret">Secret access key</Label>
              <Input
                id="repo-secret"
                type="password"
                value={form.s3_secret_key}
                onChange={(e) => set({ s3_secret_key: e.target.value })}
              />
            </div>
          </div>
        )}

        <div className="space-y-1">
          <Label htmlFor="repo-password">Repository password</Label>
          <Input
            id="repo-password"
            type="password"
            value={form.password}
            onChange={(e) => set({ password: e.target.value })}
            placeholder={form.no_password ? 'not used — repository is unencrypted' : configured ? 'retype to change anything' : ''}
            disabled={form.no_password}
          />
          {!form.no_password && (
            <p className="text-xs text-muted-foreground">
              restic encrypts with this and it cannot be recovered — without it the snapshots are unreadable, including
              by node-stats. Keep a copy somewhere outside this cluster.
            </p>
          )}

          <label className="mt-2 flex cursor-pointer items-start gap-2 text-xs">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={form.no_password}
              onChange={(e) => set({ no_password: e.target.checked, password: '' })}
            />
            <span>
              <span className="font-medium">No encryption</span> — restore the snapshots with nothing but restic, no key
              to keep and none to lose.
              {form.no_password && (
                <span className="mt-1 block text-destructive">
                  Anyone who can read the repository can read every backup — including whatever the applications hold:
                  password-manager data, <code>.env</code> files, database contents. Only sensible where the repository
                  itself is already protected.
                </span>
              )}
            </span>
          </label>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              test.mutate(undefined, {
                onSuccess: () => toast.success('Repository reachable'),
                onError: (e) => toast.error(apiError(e)),
              })
            }
            disabled={test.isPending || (!form.password && !form.no_password)}
          >
            {test.isPending && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
            Test
          </Button>
          <Button size="sm" onClick={onSave} disabled={save.isPending || (!form.password && !form.no_password)}>
            {save.isPending && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
            {configured ? 'Update repository' : 'Save repository'}
          </Button>
          {!status?.restic_installed && (
            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              <ServerCog className="h-3 w-3" /> restic is installed on save
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
