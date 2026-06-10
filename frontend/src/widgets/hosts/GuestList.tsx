import { Link } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { OSIcon } from '@/shared/components/OSIcon'
import { getHostCardTitle } from '@/shared/lib/hostDisplay'
import { cn } from '@/lib/utils'
import type { Host } from './schemas'

/** Same freshness window the backend uses (hosts.HostOfflineThreshold). */
const OFFLINE_THRESHOLD_MS = 45_000

export type GuestState = 'online' | 'offline' | 'running' | 'stopped'

/**
 * Guest liveness: agent-backed guests use agent heartbeat freshness (green /
 * red, like top-level cards); connector-only guests can't claim "online" — the
 * hypervisor only knows the power state, so they show amber "running" or grey
 * "stopped".
 */
export function guestState(host: Host): GuestState {
  const agentBacked = host.source !== 'connector'
  if (agentBacked) {
    const t = host.last_seen ? new Date(host.last_seen).getTime() : NaN
    return !isNaN(t) && Date.now() - t < OFFLINE_THRESHOLD_MS ? 'online' : 'offline'
  }
  return host.guest_status === 'running' || host.guest_status === 'online' ? 'running' : 'stopped'
}

const stateStyles: Record<GuestState, { dot: string; label: string; text: string }> = {
  online: { dot: 'bg-emerald-400 shadow-[0_0_6px_oklch(0.7_0.17_160/0.6)]', label: 'online', text: 'text-emerald-500 dark:text-emerald-400' },
  offline: { dot: 'bg-red-400', label: 'offline', text: 'text-red-400' },
  running: { dot: 'bg-amber-400 shadow-[0_0_6px_oklch(0.8_0.14_85/0.5)]', label: 'running', text: 'text-amber-500 dark:text-amber-400' },
  stopped: { dot: 'bg-zinc-400/70', label: 'stopped', text: 'text-muted-foreground' },
}

function GuestRow({ guest, compact }: { guest: Host; compact: boolean }) {
  const state = guestState(guest)
  const s = stateStyles[state]
  const title = getHostCardTitle(guest) ?? `#${guest.id}`

  return (
    <Link
      to={`/machines/${guest.id}/stats`}
      onClick={(e) => e.stopPropagation()}
      className={cn(
        'flex items-center gap-2 rounded-md px-2 py-1.5 transition-colors',
        'hover:bg-muted/40 dark:hover:bg-white/5',
        state === 'stopped' && 'opacity-60'
      )}
    >
      <OSIcon host={guest} className="h-3.5 w-3.5 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate text-xs font-medium">{title}</span>
      {!compact && guest.host_type && (
        <Badge
          variant="secondary"
          className="h-4 border border-border/50 px-1 font-mono text-[9px] uppercase text-muted-foreground"
        >
          {guest.host_type}
        </Badge>
      )}
      {!compact && guest.ipv4 && (
        <span className="hidden font-mono text-[10px] text-muted-foreground/80 sm:inline">{guest.ipv4}</span>
      )}
      <span className={cn('flex items-center gap-1 font-mono text-[10px]', s.text)}>
        <span className={cn('h-1.5 w-1.5 rounded-full', s.dot)} />
        {s.label}
        {state === 'running' && <span className="hidden text-muted-foreground/70 md:inline">(no agent)</span>}
      </span>
    </Link>
  )
}

/**
 * Minimized guest rows nested inside a hypervisor's machine card / stats page.
 * `compact` (card mode) hides the type badge and IP to keep rows one-line.
 */
export function GuestList({ guests, compact = false, className }: { guests: Host[]; compact?: boolean; className?: string }) {
  if (guests.length === 0) return null
  return (
    <div className={className}>
      <div className="mb-1 flex items-center gap-1.5 text-[9px] font-medium uppercase tracking-wider text-muted-foreground/80">
        Guests
        <span className="font-mono tabular-nums">({guests.length})</span>
      </div>
      <div
        className={cn(
          'rounded-md border border-border/50 bg-muted/15 dark:border-white/8 dark:bg-white/[0.03]',
          compact && 'max-h-40 overflow-y-auto'
        )}
      >
        <div className="divide-y divide-border/40 dark:divide-white/5">
          {guests.map((g) => (
            <GuestRow key={g.id} guest={g} compact={compact} />
          ))}
        </div>
      </div>
    </div>
  )
}
