import { Link } from 'react-router-dom'
import { Server, Wifi, WifiOff, Zap, Clock, MonitorDot } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { useHosts } from '@/widgets/hosts/useHosts'
import { useConnectionStatus } from '@/widgets/connection-status/useConnectionStatus'

function fmtUptime(u: string | null): string {
  if (!u) return '--'
  // if raw seconds number
  const n = Number(u)
  if (!isNaN(n)) {
    const d = Math.floor(n / 86400), h = Math.floor((n % 86400) / 3600), m = Math.floor((n % 3600) / 60)
    if (d > 0) return `${d}d ${h}h`
    if (h > 0) return `${h}h ${m}m`
    return `${m}m`
  }
  return u.split('.')[0]
}

function fmtLatency(ms: number | null): string {
  if (ms == null || ms < 0) return '--'
  if (ms < 1) return '<1ms'
  return `${Math.round(ms)}ms`
}

function HostCard({ host }: { host: any }) {
  const { isConnected, latency, uptime, isLoading: connLoading } = useConnectionStatus(host.id)

  return (
    <Link to={`/machines/${host.id}/stats`} className="group block">
      <div className={cn(
        'relative flex flex-col rounded-xl border bg-card overflow-hidden',
        'transition-all duration-200',
        'hover:border-primary/30 hover:shadow-lg hover:shadow-black/10 hover:-translate-y-0.5',
        isConnected ? 'border-border' : 'border-border opacity-70'
      )}>
        {/* Status stripe */}
        <div className={cn(
          'absolute top-0 left-0 right-0 h-0.5',
          isConnected ? 'bg-green-500' : 'bg-muted-foreground/30'
        )} />

        <div className="p-4 pt-5 flex-1">
          {/* Header */}
          <div className="flex items-start justify-between gap-3 mb-3">
            <div className="min-w-0">
              <h2 className="font-semibold text-base leading-tight truncate group-hover:text-primary transition-colors">
                {host.name}
              </h2>
              {(host.platform || host.os) && (
                <p className="text-xs text-muted-foreground mt-0.5">
                  {host.platform || host.os}{host.platform_version ? ` ${host.platform_version}` : ''}
                </p>
              )}
            </div>
            <span className={cn(
              'shrink-0 mt-0.5',
              isConnected ? 'text-green-500' : 'text-muted-foreground/40'
            )}>
              {isConnected ? <Wifi className="h-4 w-4" /> : <WifiOff className="h-4 w-4" />}
            </span>
          </div>

          {/* Info rows */}
          <div className="space-y-1.5 text-xs">
            {host.ipv4 && (
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">IPv4</span>
                <span className="font-mono text-[11px]">{host.ipv4}</span>
              </div>
            )}
            {host.kernel_version && (
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Kernel</span>
                <span className="truncate max-w-[55%] text-right">{host.kernel_version}</span>
              </div>
            )}
            {host.virtualization_system && (
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Virt</span>
                <Badge variant="secondary" className="text-[10px] py-0 px-1.5 h-4">
                  {host.virtualization_system}
                </Badge>
              </div>
            )}
            {host.last_seen && (
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Last seen</span>
                <span className="text-[11px]">{new Date(host.last_seen).toLocaleString()}</span>
              </div>
            )}
          </div>
        </div>

        {/* Footer */}
        <div className="px-4 py-2.5 border-t border-border bg-muted/30 flex items-center gap-4 text-xs text-muted-foreground">
          {connLoading ? (
            <>
              <Skeleton className="h-3 w-12" />
              <Skeleton className="h-3 w-14" />
            </>
          ) : (
            <>
              <span className="flex items-center gap-1.5">
                <Zap className="h-3 w-3 text-amber-500" />
                {fmtLatency(latency)}
              </span>
              <span className="flex items-center gap-1.5">
                <Clock className="h-3 w-3 text-sky-500" />
                {fmtUptime(uptime)}
              </span>
            </>
          )}
        </div>
      </div>
    </Link>
  )
}

export function MachineListPage() {
  const { data: hostsData, isLoading } = useHosts()
  const hosts: any[] = hostsData?.hosts ?? []

  return (
    <div className="mx-auto max-w-7xl px-4 py-8">
      <div className="flex items-center gap-3 mb-6">
        <MonitorDot className="h-5 w-5 text-muted-foreground" />
        <h1 className="text-lg font-semibold">Machines</h1>
        {!isLoading && (
          <Badge variant="secondary" className="tabular-nums">{hosts.length}</Badge>
        )}
      </div>

      {isLoading ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map(i => (
            <div key={i} className="rounded-xl border border-border bg-card p-4 space-y-3">
              <Skeleton className="h-5 w-32" />
              <Skeleton className="h-3 w-20" />
              <div className="space-y-2 pt-1">
                {[1, 2, 3].map(j => <Skeleton key={j} className="h-3 w-full" />)}
              </div>
            </div>
          ))}
        </div>
      ) : hosts.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 text-center">
          <Server className="h-14 w-14 text-muted-foreground/20 mb-4" />
          <p className="text-muted-foreground">No machines registered yet.</p>
          <p className="text-xs text-muted-foreground/60 mt-1">
            Start the agent on a machine to see it here.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
          {hosts.map(host => <HostCard key={host.id} host={host} />)}
        </div>
      )}
    </div>
  )
}
