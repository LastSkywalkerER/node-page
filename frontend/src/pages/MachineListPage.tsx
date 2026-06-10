import { Link } from 'react-router-dom'
import { AreaChart, Area } from 'recharts'
import { Server, Wifi, WifiOff, Zap, Clock, MonitorDot } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { ChartContainer, type ChartConfig } from '@/components/ui/chart'
import { cn } from '@/lib/utils'
import { useHosts } from '@/widgets/hosts/useHosts'
import { useConnectionStatus } from '@/widgets/connection-status/useConnectionStatus'
import { useCPU } from '@/widgets/cpu/useCPU'
import { useMemory } from '@/widgets/memory/useMemory'
import { useDisk } from '@/widgets/disk/useDisk'
import { useNetwork } from '@/widgets/network/useNetwork'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { formatBytes } from '@/shared/lib/utils'
import type { Host } from '@/widgets/hosts/schemas'
import { getHostCardTitle } from '@/shared/lib/hostDisplay'
import { AllApplicationsSection } from '@/widgets/applications/AllApplicationsSection'

const LIVE = { mode: 'poll' } as const

function fmtLatency(ms: number | null): string {
  if (ms == null || ms < 0) return '--'
  if (ms < 1) return '<1ms'
  return `${Math.round(ms)}ms`
}

// usageColor shades a percentage value: calm metric colour, amber when high,
// red when critical.
function usageColor(pct: number | null, base: string): string {
  if (pct == null) return base
  if (pct >= 90) return '#ef4444'
  if (pct >= 75) return '#f59e0b'
  return base
}

function netSpeedKbps(interfaces?: { speed_kbps_recv: number; speed_kbps_sent: number }[]): number {
  if (!Array.isArray(interfaces)) return 0
  return interfaces.reduce((s, i) => s + (i.speed_kbps_recv || 0) + (i.speed_kbps_sent || 0), 0)
}

function fmtSpeed(kbps: number): string {
  if (kbps >= 1000) return `${(kbps / 1000).toFixed(1)} Mb/s`
  return `${Math.round(kbps)} kb/s`
}

/** Minimalist sparkline — no axes, no tooltip, fills with a soft gradient. */
function Spark({ id, data, color }: { id: string; data: number[]; color: string }) {
  const config: ChartConfig = { v: { color } }
  const chartData = data.length ? data.map((v, i) => ({ i, v })) : [{ i: 0, v: 0 }]
  return (
    <ChartContainer config={config} className="h-8 w-full">
      <AreaChart data={chartData} margin={{ top: 2, right: 0, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--color-v)" stopOpacity={0.3} />
            <stop offset="100%" stopColor="var(--color-v)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <Area
          type="monotone"
          dataKey="v"
          stroke="var(--color-v)"
          fill={`url(#${id})`}
          strokeWidth={1.4}
          isAnimationActive={false}
          dot={false}
        />
      </AreaChart>
    </ChartContainer>
  )
}

function MetricTile({
  id,
  label,
  value,
  sub,
  data,
  color,
}: {
  id: string
  label: string
  value: string
  sub?: string
  data: number[]
  color: string
}) {
  return (
    <div className="rounded-md border border-border/50 bg-muted/15 px-2 pt-1.5 pb-1 dark:border-white/8 dark:bg-white/[0.03]">
      <div className="flex items-baseline justify-between gap-1">
        <span className="text-[9px] font-medium uppercase tracking-wider text-muted-foreground">{label}</span>
        <span className="font-mono text-xs font-semibold tabular-nums" style={{ color }}>
          {value}
        </span>
      </div>
      {sub && (
        <div className="truncate text-right font-mono text-[9px] leading-tight tabular-nums text-muted-foreground/70">
          {sub}
        </div>
      )}
      <Spark id={id} data={data} color={color} />
    </div>
  )
}

function HostMetrics({ hostId }: { hostId: number }) {
  const { data: cpu } = useCPU(hostId, LIVE)
  const { data: mem } = useMemory(hostId, LIVE)
  const { data: disk } = useDisk(hostId, LIVE)
  const { data: net } = useNetwork(hostId, LIVE)

  const cpuPct = cpu?.latest?.usage_percent ?? null
  const memPct = mem?.latest?.usage_percent ?? null
  const diskPct = disk?.latest?.usage_percent ?? null
  const netNow = netSpeedKbps(net?.latest?.interfaces)

  const cpuCores = cpu?.latest?.cores
  const cpuSub = cpuCores ? `${cpuCores} cores` : undefined
  const memSub =
    mem?.latest?.used != null && mem?.latest?.total != null
      ? `${formatBytes(mem.latest.used)} / ${formatBytes(mem.latest.total)}`
      : undefined
  const diskSub =
    disk?.latest?.used != null && disk?.latest?.total != null
      ? `${formatBytes(disk.latest.used)} / ${formatBytes(disk.latest.total)}`
      : undefined

  const cpuSeries = (cpu?.history ?? []).map((p) => p.usage)
  const memSeries = (mem?.history ?? []).map((p) => p.usage_percent)
  const diskSeries = (disk?.history ?? []).map((p) => p.usage_percent)
  const netSeries = (net?.history ?? []).map((p) => netSpeedKbps(p.interfaces))

  return (
    <div className="grid grid-cols-2 gap-1.5">
      <MetricTile
        id={`spk-${hostId}-cpu`}
        label="CPU"
        value={cpuPct == null ? '--' : `${cpuPct.toFixed(0)}%`}
        sub={cpuSub}
        data={cpuSeries}
        color={usageColor(cpuPct, CHART_COLORS.cpu)}
      />
      <MetricTile
        id={`spk-${hostId}-mem`}
        label="RAM"
        value={memPct == null ? '--' : `${memPct.toFixed(0)}%`}
        sub={memSub}
        data={memSeries}
        color={usageColor(memPct, CHART_COLORS.memory)}
      />
      <MetricTile
        id={`spk-${hostId}-disk`}
        label="Disk"
        value={diskPct == null ? '--' : `${diskPct.toFixed(0)}%`}
        sub={diskSub}
        data={diskSeries}
        color={usageColor(diskPct, CHART_COLORS.disk)}
      />
      <MetricTile
        id={`spk-${hostId}-net`}
        label="Net"
        value={net?.latest ? fmtSpeed(netNow) : '--'}
        data={netSeries}
        color={CHART_COLORS.network}
      />
    </div>
  )
}

function MetaCell({ label, value, mono = true }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-[9px] uppercase tracking-wider text-muted-foreground/80">{label}</div>
      <div className={cn('truncate text-[11px] leading-tight', mono && 'font-mono')} title={value}>
        {value}
      </div>
    </div>
  )
}

function HostCard({ host }: { host: Host }) {
  const { isConnected, latency, uptime, showUptime, isLoading: connLoading } = useConnectionStatus(host.id)
  const cardTitle = getHostCardTitle(host)

  return (
    <Link to={`/machines/${host.id}/stats`} className="group block cursor-pointer">
      <div
        className={cn(
          'relative transition-all duration-300 ease-out hover:-translate-y-0.5',
          'hover:shadow-md hover:shadow-black/8 dark:hover:shadow-[0_0_56px_-14px_oklch(0.72_0.22_320/0.22)]'
        )}
      >
        <div
          className={cn(
            'cyber-frame relative flex flex-col rounded-xl backdrop-blur-xl backdrop-saturate-150',
            'border border-border/60 dark:border-white/10 overflow-hidden bg-card',
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

          <div className="relative z-2 flex-1 p-3 pt-5">
            {/* Header */}
            <div className="mb-2.5 flex items-start justify-between gap-2">
              <div className="min-w-0">
                {cardTitle && (
                  <h2 className="truncate font-display text-sm font-semibold leading-tight tracking-wide transition-colors duration-200 group-hover:text-primary">
                    {cardTitle}
                  </h2>
                )}
                {(host.platform || host.os) && (
                  <p className="truncate font-mono text-[10px] text-muted-foreground">
                    {host.platform || host.os}
                    {host.platform_version ? ` ${host.platform_version}` : ''}
                  </p>
                )}
              </div>
              <span
                className={cn(
                  'mt-0.5 shrink-0 transition-colors duration-200',
                  isConnected ? 'text-cyan-400 drop-shadow-[0_0_8px_oklch(0.72_0.16_195/0.6)]' : 'text-red-400'
                )}
              >
                {isConnected ? <Wifi className="h-4 w-4" /> : <WifiOff className="h-4 w-4" />}
              </span>
            </div>

            {/* Live metrics */}
            <HostMetrics hostId={host.id} />

            {/* Compact metadata */}
            <div className="mt-2.5 grid grid-cols-2 gap-x-3 gap-y-1.5">
              {host.ipv4 && <MetaCell label="IPv4" value={host.ipv4} />}
              {host.virtualization_system && <MetaCell label="Virt" value={host.virtualization_system} />}
              {host.kernel_version && <MetaCell label="Kernel" value={host.kernel_version} />}
              {host.mac_address && <MetaCell label="MAC" value={host.mac_address} />}
            </div>
          </div>

          {/* Footer */}
          <div
            className={cn(
              'relative z-2 flex items-center gap-4 border-t px-3 py-2 text-xs text-muted-foreground',
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
                {showUptime && uptime && (
                  <span className="flex items-center gap-1.5 font-mono">
                    <Clock className="h-3 w-3 text-cyan-400/90" />
                    {uptime}
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
    <div className="mx-auto max-w-7xl space-y-10 px-4 py-8 md:py-10">
      <section>
        <div className="flex items-center gap-3 mb-5">
          <MonitorDot className="h-6 w-6 text-primary drop-shadow-[0_0_10px_oklch(0.72_0.16_195/0.45)]" />
          <h2 className="text-xl md:text-2xl font-display font-semibold tracking-wide uppercase">Nodes</h2>
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
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {[1, 2, 3, 4].map((i) => (
              <div
                key={i}
                className="rounded-xl border border-border/60 bg-card/40 backdrop-blur-lg p-3 space-y-3 dark:border-white/10"
              >
                <Skeleton className="h-4 w-28" />
                <div className="grid grid-cols-2 gap-1.5 pt-1">
                  {[1, 2, 3, 4].map((j) => (
                    <Skeleton key={j} className="h-12 w-full" />
                  ))}
                </div>
              </div>
            ))}
          </div>
        ) : hosts.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-24 text-center rounded-2xl border border-dashed border-border/70 bg-card/30 backdrop-blur-md dark:border-white/15 px-6">
            <Server className="h-16 w-16 text-muted-foreground/25 mb-5" />
            <p className="text-muted-foreground font-display tracking-wide">No machines registered yet.</p>
            <p className="text-xs text-muted-foreground/70 mt-2 max-w-sm font-mono">
              Add a node with a connect key from the setup wizard to see it here.
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {hosts.map((host) => (
              <HostCard key={host.id} host={host} />
            ))}
          </div>
        )}
      </section>

      <AllApplicationsSection />
    </div>
  )
}
