import { format, formatDistanceToNow } from 'date-fns'
import { Database, HardDrive, CheckCircle2, AlertTriangle, Loader2 } from 'lucide-react'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { formatBytes } from '@/shared/lib/utils'
import { usePBS } from './usePBS'
import type { PBSTask, PBSGroup } from './schemas'

function barColor(pct: number): string {
  if (pct >= 90) return '#ef4444'
  if (pct >= 75) return '#f59e0b'
  return '#10b981'
}

/** Unix seconds → "HH:mm dd.MM.yyyy", "" when zero/invalid. */
function fmtUnix(sec: number): string {
  if (!sec) return ''
  const d = new Date(sec * 1000)
  return isNaN(d.getTime()) ? '' : format(d, 'HH:mm dd.MM.yyyy')
}

function fmtAgo(sec: number): string {
  if (!sec) return ''
  const d = new Date(sec * 1000)
  return isNaN(d.getTime()) ? '' : formatDistanceToNow(d, { addSuffix: true })
}

function taskStatusStyle(status: string): { dot: string; text: string } {
  if (status === 'OK') return { dot: 'bg-emerald-400', text: 'text-muted-foreground' }
  if (status === 'running') return { dot: 'bg-amber-400 animate-pulse', text: 'text-amber-500 dark:text-amber-400' }
  return { dot: 'bg-red-400', text: 'text-red-400' }
}

/** A backed-up machine: vm/ct/host group with its last-backup time + count. */
function GroupRow({ group }: { group: PBSGroup }) {
  const stale = group.last_backup > 0 && Date.now() / 1000 - group.last_backup > 48 * 3600
  return (
    <div className="flex items-center gap-2 py-1 text-xs">
      <span className="w-10 shrink-0 rounded bg-muted/60 px-1 text-center font-mono text-[9px] uppercase text-muted-foreground">
        {group.backup_type || '—'}
      </span>
      <span className="min-w-0 flex-1 truncate font-medium" title={`${group.backup_type}/${group.backup_id} on ${group.datastore}`}>
        {group.backup_id || '—'}
        <span className="ml-1 font-mono text-[10px] text-muted-foreground/60">{group.datastore}</span>
      </span>
      <span className="w-16 shrink-0 text-right font-mono tabular-nums text-muted-foreground" title="Last backup size">
        {group.last_size > 0 ? formatBytes(group.last_size) : '—'}
      </span>
      <span className="hidden w-16 shrink-0 text-right font-mono tabular-nums text-muted-foreground/70 sm:inline">{group.count} snap</span>
      <span className={cn('w-28 shrink-0 truncate text-right', stale ? 'text-amber-500 dark:text-amber-400' : 'text-muted-foreground')} title={fmtUnix(group.last_backup)}>
        {group.last_backup > 0 ? fmtAgo(group.last_backup) : 'never'}
      </span>
    </div>
  )
}

function TaskRow({ task }: { task: PBSTask }) {
  const s = taskStatusStyle(task.status)
  return (
    <div className="flex items-center gap-2 py-1 text-xs">
      <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', s.dot)} />
      <span className="w-28 shrink-0 truncate font-mono uppercase text-muted-foreground/80">{task.type}</span>
      <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={task.id}>{task.id || '—'}</span>
      <span className="shrink-0 font-mono tabular-nums text-muted-foreground/70">{fmtUnix(task.start_time)}</span>
      <span className={cn('w-16 shrink-0 truncate text-right font-mono', s.text)} title={task.status}>
        {task.status === 'OK' ? 'ok' : task.status === 'running' ? 'running' : 'error'}
      </span>
    </div>
  )
}

/**
 * Proxmox Backup Server detail: per-datastore capacity + backup health. Node
 * CPU/mem/disk come from the normal metric widgets; this adds the PBS-specific
 * surface. Renders nothing until the polling node has a snapshot.
 */
export function PBSWidget({ hostId }: { hostId: number }) {
  const { data } = usePBS(hostId)
  const datastores = data?.datastores ?? []
  const groups = data?.groups ?? []
  const backups = data?.backups
  const hasData = datastores.length > 0 || groups.length > 0 || (backups?.recent_tasks?.length ?? 0) > 0
  if (!hasData) return null

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center gap-2">
          <Database className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="text-sm font-medium text-muted-foreground">Backup Server</span>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 pt-0">
        {datastores.length > 0 && (
          <div className="space-y-3">
            {datastores.map((ds) => (
              <div key={ds.store} className="space-y-1">
                <div className="flex items-center justify-between gap-2 text-xs">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <HardDrive className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    <span className="truncate font-medium">{ds.store}</span>
                  </span>
                  <span className="shrink-0 font-mono tabular-nums text-muted-foreground">
                    {formatBytes(ds.used)} / {formatBytes(ds.total)} ({ds.usage_percent.toFixed(0)}%)
                  </span>
                </div>
                <div className="h-1 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full transition-all duration-500"
                    style={{ width: `${Math.min(100, ds.usage_percent)}%`, backgroundColor: barColor(ds.usage_percent) }}
                  />
                </div>
                {ds.error && <div className="text-[11px] text-red-400">{ds.error}</div>}
              </div>
            ))}
          </div>
        )}

        {groups.length > 0 && (
          <div className="space-y-1 border-t border-border/40 pt-3 dark:border-white/5">
            <div className="mb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/80">
              Backed-up machines <span className="font-mono tabular-nums">({groups.length})</span>
            </div>
            <div className="max-h-48 divide-y divide-border/30 overflow-y-auto dark:divide-white/5">
              {groups.map((g) => (
                <GroupRow key={`${g.datastore}/${g.backup_type}/${g.backup_id}`} group={g} />
              ))}
            </div>
          </div>
        )}

        {backups && (backups.last_backup_at > 0 || backups.running_tasks > 0 || backups.last_error || (backups.recent_tasks?.length ?? 0) > 0) && (
          <div className="space-y-2 border-t border-border/40 pt-3 dark:border-white/5">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
              {backups.last_backup_at > 0 && (
                <span className="flex items-center gap-1.5 text-muted-foreground">
                  <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
                  Last backup {fmtAgo(backups.last_backup_at)}
                  <span className="font-mono text-muted-foreground/60">({fmtUnix(backups.last_backup_at)})</span>
                </span>
              )}
              {backups.running_tasks > 0 && (
                <span className="flex items-center gap-1.5 text-amber-500 dark:text-amber-400">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {backups.running_tasks} running
                </span>
              )}
            </div>
            {backups.last_error && (
              <div className="flex items-start gap-1.5 text-xs text-red-400">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span className="min-w-0">
                  <span className="font-medium">Last error</span> {fmtAgo(backups.last_error_at)}:{' '}
                  <span className="break-words font-mono">{backups.last_error}</span>
                </span>
              </div>
            )}
            {(backups.recent_tasks?.length ?? 0) > 0 && (
              <div className="divide-y divide-border/30 dark:divide-white/5">
                {backups.recent_tasks!.map((t, i) => (
                  <TaskRow key={`${t.type}-${t.start_time}-${i}`} task={t} />
                ))}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
