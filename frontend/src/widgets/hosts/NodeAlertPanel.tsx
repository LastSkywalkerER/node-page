import { Unplug } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { NodeAlert } from '@/widgets/hosts/schemas'

/** Short human duration since an ISO timestamp ("3h", "2d"); '' when unusable. */
function sinceLabel(iso: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (isNaN(t)) return ''
  const secs = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h`
  return `${Math.floor(secs / 86400)}d`
}

/**
 * Renders one node self-diagnosed fault (hosts.NodeAlert): what the node
 * observed about itself and, in order of preference, how to re-attach it.
 *
 * Shown in two places — the machine list (for any machine whose node reported
 * one over the metric stream) and the admin Raft panel of the affected node
 * itself (from GET /raft/status), so the operator meets the same wording and
 * the same steps wherever they land.
 */
export function NodeAlertPanel({
  alert,
  machine,
  className,
}: {
  alert: NodeAlert
  /** Machine label, when the panel is shown away from that machine's card. */
  machine?: string
  className?: string
}) {
  const error = alert.severity !== 'warning'
  const since = sinceLabel(alert.since)
  const facts: Array<[string, string]> = []
  if (alert.node_id) facts.push(['node', alert.node_id])
  if (alert.advertise_addr) facts.push(['advertises', alert.advertise_addr])
  if (alert.local_ipv4) facts.push(['actually at', alert.local_ipv4])
  if (since) facts.push(['for', since])

  return (
    <div
      className={cn(
        'rounded-md border p-3 space-y-2 text-sm',
        error
          ? 'border-rose-500/50 bg-rose-500/10'
          : 'border-amber-500/50 bg-amber-500/10',
        className
      )}
    >
      <p className={cn('flex items-center gap-2 font-medium', error ? 'text-rose-200' : 'text-amber-200')}>
        <Unplug className="h-4 w-4 shrink-0" />
        <span>
          {machine ? `${machine}: ` : ''}
          {alert.title}
        </span>
      </p>

      {alert.detail && (
        <p className={cn('text-xs', error ? 'text-rose-100/90' : 'text-amber-100/90')}>{alert.detail}</p>
      )}

      {facts.length > 0 && (
        <div
          className={cn(
            'rounded border bg-black/20 p-2 font-mono text-[11px] space-y-0.5',
            error ? 'border-rose-500/30 text-rose-100/80' : 'border-amber-500/30 text-amber-100/80'
          )}
        >
          {facts.map(([k, v]) => (
            <p key={k}>
              {k}: <span className={error ? 'text-rose-50' : 'text-amber-50'}>{v}</span>
            </p>
          ))}
        </div>
      )}

      {alert.steps.length > 0 && (
        <ol className={cn('list-decimal space-y-1 pl-4 text-xs', error ? 'text-rose-100/90' : 'text-amber-100/90')}>
          {alert.steps.map((s) => (
            <li key={s} className="break-words">
              {s}
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
