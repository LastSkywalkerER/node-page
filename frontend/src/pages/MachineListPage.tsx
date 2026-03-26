import { Link } from 'react-router-dom'
import { Server, Wifi, WifiOff, Zap, Clock, MonitorDot } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { useHosts } from '@/widgets/hosts/useHosts'
import { useConnectionStatus } from '@/widgets/connection-status/useConnectionStatus'
import type { Host } from '@/widgets/hosts/schemas'
import { getHostCardTitle } from '@/shared/lib/hostDisplay'

function fmtUptime(u: string | null): string {
  if (!u) return '--'
  return u
}

function fmtLatency(ms: number | null): string {
  if (ms == null || ms < 0) return '--'
  if (ms < 1) return '<1ms'
  return `${Math.round(ms)}ms`
}

function HostCard({ host }: { host: Host }) {
  const { isConnected, latency, uptime, showUptime, isLoading: connLoading } = useConnectionStatus(host.id)
  const cardTitle = getHostCardTitle(host)

  return (
    <Link to={`/machines/${host.id}/stats`} className="group block cursor-pointer">
      <div
        className={cn(
          'relative transition-all duration-300 ease-out',
          'hover:-translate-y-0.5',
          'hover:shadow-md hover:shadow-black/8 dark:hover:shadow-[0_0_56px_-14px_oklch(0.72_0.22_320/0.22)]'
        )}
      >
        <div
          className={cn(
            'cyber-frame relative flex flex-col rounded-xl backdrop-blur-xl backdrop-saturate-150',
            'border border-border/60 dark:border-white/10 overflow-hidden',
            'bg-card',
            /* Slightly dimmer glass when offline — do NOT use opacity on a parent (breaks backdrop-filter) */
            isConnected ? '' : 'bg-card/92 dark:bg-card/75 ring-1 ring-inset ring-red-500/15 dark:ring-red-400/20'
          )}
        >
          <div
            className={cn(
              'absolute top-0 left-0 right-0 h-[3px] z-3',
              isConnected
                ? 'bg-linear-to-r from-emerald-500 via-teal-400 to-cyan-400 shadow-[0_0_14px_oklch(0.65_0.18_160/0.55)]'
                : 'bg-linear-to-r from-red-600 via-rose-500 to-orange-500 shadow-[0_0_12px_oklch(0.55_0.22_25/0.5)]'
            )}
          />
          <div className="relative z-2 flex-1 p-4 pt-7">
            <div className="flex items-start justify-between gap-3 mb-3">
              <div className="min-w-0">
                {cardTitle && (
                  <h2 className="font-display font-semibold text-base leading-tight truncate group-hover:text-primary transition-colors duration-200 tracking-wide">
                    {cardTitle}
                  </h2>
                )}
                {(host.platform || host.os) && (
                  <p className={cn('text-xs text-muted-foreground font-mono', cardTitle ? 'mt-0.5' : '')}>
                    {host.platform || host.os}{host.platform_version ? ` ${host.platform_version}` : ''}
                  </p>
                )}
              </div>
              <span
                className={cn(
                  'shrink-0 mt-0.5 transition-colors duration-200',
                  isConnected ? 'text-cyan-400 drop-shadow-[0_0_8px_oklch(0.72_0.16_195/0.6)]' : 'text-red-400'
                )}
              >
                {isConnected ? <Wifi className="h-4 w-4" /> : <WifiOff className="h-4 w-4" />}
              </span>
            </div>

            <div className="space-y-1.5 text-xs">
              {host.ipv4 && (
                <div className="flex justify-between items-center gap-2">
                  <span className="text-muted-foreground uppercase tracking-wider text-[10px]">IPv4</span>
                  <span className="font-mono text-[11px] text-right">{host.ipv4}</span>
                </div>
              )}
              {host.mac_address && (
                <div className="flex justify-between items-center gap-2">
                  <span className="text-muted-foreground shrink-0 uppercase tracking-wider text-[10px]">MAC</span>
                  <span className="font-mono text-[11px] text-right break-all min-w-0">
                    {host.mac_address}
                  </span>
                </div>
              )}
              {host.kernel_version && (
                <div className="flex justify-between items-center gap-2">
                  <span className="text-muted-foreground uppercase tracking-wider text-[10px]">Kernel</span>
                  <span className="truncate max-w-[55%] text-right text-[11px]">{host.kernel_version}</span>
                </div>
              )}
              {host.virtualization_system && (
                <div className="flex justify-between items-center">
                  <span className="text-muted-foreground uppercase tracking-wider text-[10px]">Virt</span>
                  <Badge variant="secondary" className="text-[10px] py-0 px-1.5 h-4 font-mono border border-border/50">
                    {host.virtualization_system}
                  </Badge>
                </div>
              )}
              {host.last_seen && (
                <div className="flex justify-between items-center gap-2">
                  <span className="text-muted-foreground uppercase tracking-wider text-[10px]">Last seen</span>
                  <span className="text-[11px] font-mono text-right">{new Date(host.last_seen).toLocaleString()}</span>
                </div>
              )}
            </div>
          </div>

          <div
            className={cn(
              'relative z-2 px-4 py-2.5 border-t flex items-center gap-4 text-xs text-muted-foreground',
              'border-border/60 bg-muted/25 backdrop-blur-md dark:border-white/8 dark:bg-black/20'
            )}
          >
            {connLoading ? (
              <>
                <Skeleton className="h-3 w-12" />
                <Skeleton className="h-3 w-14" />
              </>
            ) : (
              <>
                <span className="flex items-center gap-1.5 font-mono">
                  <Zap className="h-3 w-3 text-amber-400 drop-shadow-[0_0_6px_oklch(0.8_0.14_85/0.45)]" />
                  {fmtLatency(latency)}
                </span>
                {showUptime && (
                  <span className="flex items-center gap-1.5 font-mono">
                    <Clock className="h-3 w-3 text-cyan-400/90" />
                    {fmtUptime(uptime)}
                  </span>
                )}
              </>
            )}
          </div>
        </div>
      </div>
    </Link>
  )
}

export function MachineListPage() {
  const { data: hostsData, isLoading } = useHosts()
  const hosts: Host[] = hostsData?.hosts ?? []

  return (
    <div className="mx-auto max-w-7xl px-4 py-8 md:py-10">
      <div className="flex items-center gap-3 mb-8">
        <MonitorDot className="h-6 w-6 text-primary drop-shadow-[0_0_10px_oklch(0.72_0.16_195/0.45)]" />
        <h1 className="text-xl md:text-2xl font-display font-semibold tracking-wide uppercase">Machines</h1>
        {!isLoading && (
          <Badge
            variant="secondary"
            className="tabular-nums font-mono border border-border/60 bg-primary/10 text-primary dark:bg-cyan-500/10 dark:text-cyan-200 dark:border-cyan-500/25"
          >
            {hosts.length}
          </Badge>
        )}
      </div>

      {isLoading ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-5">
          {[1, 2, 3, 4].map(i => (
            <div
              key={i}
              className="rounded-xl border border-border/60 bg-card/40 backdrop-blur-lg p-4 space-y-3 dark:border-white/10"
            >
              <Skeleton className="h-5 w-32" />
              <Skeleton className="h-3 w-20" />
              <div className="space-y-2 pt-1">
                {[1, 2, 3].map(j => <Skeleton key={j} className="h-3 w-full" />)}
              </div>
            </div>
          ))}
        </div>
      ) : hosts.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 text-center rounded-2xl border border-dashed border-border/70 bg-card/30 backdrop-blur-md dark:border-white/15 px-6">
          <Server className="h-16 w-16 text-muted-foreground/25 mb-5" />
          <p className="text-muted-foreground font-display tracking-wide">No machines registered yet.</p>
          <p className="text-xs text-muted-foreground/70 mt-2 max-w-sm font-mono">
            Start the agent on a machine to see it here.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-5">
          {hosts.map(host => <HostCard key={host.id} host={host} />)}
        </div>
      )}
    </div>
  )
}
