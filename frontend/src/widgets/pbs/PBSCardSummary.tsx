import { formatDistanceToNow } from 'date-fns'
import { DatabaseBackup } from 'lucide-react'
import { cn } from '@/lib/utils'
import { formatBytes } from '@/shared/lib/utils'
import { usePBS } from './usePBS'
import type { PBSGroup } from './schemas'

// How many backed-up machines to surface on the compact machine card.
const MAX_ROWS = 5

/** Unix seconds → relative ("2 hours ago"), "never" when zero/invalid. */
function fmtAgo(sec: number): string {
  if (!sec) return 'never'
  const d = new Date(sec * 1000)
  return isNaN(d.getTime()) ? '' : formatDistanceToNow(d, { addSuffix: true })
}

function BackupRow({ group }: { group: PBSGroup }) {
  // Older than two days without a fresh backup is worth flagging.
  const stale = group.last_backup > 0 && Date.now() / 1000 - group.last_backup > 48 * 3600
  return (
    <div className="flex items-center gap-2 px-2 py-1 text-xs">
      <span className="w-7 shrink-0 rounded bg-muted/60 px-1 text-center font-mono text-[9px] uppercase text-muted-foreground">
        {group.backup_type || '—'}
      </span>
      <span className="min-w-0 flex-1 truncate font-medium" title={`${group.backup_type}/${group.backup_id} on ${group.datastore}`}>
        {group.backup_id || '—'}
      </span>
      <span className="shrink-0 font-mono text-[10px] tabular-nums text-muted-foreground">
        {group.last_size > 0 ? formatBytes(group.last_size) : '—'}
      </span>
      <span
        className={cn(
          'w-20 shrink-0 truncate text-right font-mono text-[10px] tabular-nums',
          stale ? 'text-amber-500 dark:text-amber-400' : 'text-muted-foreground/70'
        )}
      >
        {fmtAgo(group.last_backup)}
      </span>
    </div>
  )
}

/**
 * Compact backup overview nested in a PBS host's machine card: the most-recently
 * backed-up machines with the size and age of their last backup. Mirrors the
 * GuestList compact style; renders nothing until the polling node has a snapshot.
 * Only mount this for `platform === 'proxmox-backup-server'` hosts.
 */
export function PBSCardSummary({ hostId, className }: { hostId: number; className?: string }) {
  const { data } = usePBS(hostId)
  const groups = data?.groups ?? []
  if (groups.length === 0) return null
  // Groups already arrive newest-backup-first from the poller.
  const rows = groups.slice(0, MAX_ROWS)

  return (
    <div className={className}>
      <div className="mb-1 flex items-center gap-1.5 text-[9px] font-medium uppercase tracking-wider text-muted-foreground/80">
        <DatabaseBackup className="h-3 w-3" />
        Backups
        <span className="font-mono tabular-nums">({groups.length})</span>
      </div>
      <div className="max-h-40 divide-y divide-border/40 overflow-y-auto rounded-md border border-border/50 bg-muted/15 dark:divide-white/5 dark:border-white/8 dark:bg-white/[0.03]">
        {rows.map((g) => (
          <BackupRow key={`${g.datastore}/${g.backup_type}/${g.backup_id}`} group={g} />
        ))}
      </div>
    </div>
  )
}
