import { Link } from 'react-router-dom'
import { AreaChart, Area } from 'recharts'
import { Server, Wifi, WifiOff, Zap, Clock, MonitorDot } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { ChartContainer, type ChartConfig } from '@/components/ui/chart'
import { cn } from '@/lib/utils'
import { useHosts } from '@/widgets/hosts/useHosts'
import { useHostGaugesStream } from '@/shared/hooks/useEventSource'
import { useConnectionStatus } from '@/widgets/connection-status/useConnectionStatus'
import { useCPU } from '@/widgets/cpu/useCPU'
import { useMemory } from '@/widgets/memory/useMemory'
import { useDisk } from '@/widgets/disk/useDisk'
import { useNetwork } from '@/widgets/network/useNetwork'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { formatBytes } from '@/shared/lib/utils'
import type { Host } from '@/widgets/hosts/schemas'
import { GuestList } from '@/widgets/hosts/GuestList'
import { PBSCardSummary } from '@/widgets/pbs/PBSCardSummary'
import { usePBS } from '@/widgets/pbs/usePBS'
import { OSIcon } from '@/shared/components/OSIcon'
import { getHostCardTitle } from '@/shared/lib/hostDisplay'
import { AllApplicationsSection } from '@/widgets/applications/AllApplicationsSection'

// While the SSE stream is connected the cards update live from the per-host
// metric caches (one REST load on mount), so no interval polling. If SSE drops,
// fall back to a slow 30s poll — machine list metrics don't need 5s granularity.
const SSE_LIVE = { mode: 'snapshot' } as const
const SSE_FALLBACK = { mode: 'poll', pollMs: 30_000 } as const

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

// Connector hosts without a readable NIC get a synthetic "proxmox:…" identity
// in mac_address — not worth showing as a MAC.
function isRealMac(mac: string): boolean {
  return /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/i.test(mac)
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

function HostMetrics({ host, live }: { host: Host; live: boolean }) {
  const hostId = host.id
  const opts = live ? SSE_LIVE : SSE_FALLBACK
  const { data: cpu } = useCPU(hostId, opts)
  const { data: mem } = useMemory(hostId, opts)
  const { data: disk } = useDisk(hostId, opts)
  const { data: net } = useNetwork(hostId, opts)

  const cpuPct = cpu?.latest?.usage_percent ?? null
  const memPct = mem?.latest?.usage_percent ?? null
  const diskPct = disk?.latest?.usage_percent ?? null
  const netNow = netSpeedKbps(net?.latest?.interfaces)

  // Static parts (cores, totals) come from the host row served by /hosts; the
  // live parts (used, %) come from the metric stream.
  const cpuCores = host.cpu_cores || cpu?.latest?.cores
  const cpuSub = cpuCores ? `${cpuCores} cores` : undefined
  const memTotal = host.memory_total || mem?.latest?.total
  const memSub = memTotal
    ? `${mem?.latest?.used != null ? formatBytes(mem.latest.used) : '--'} / ${formatBytes(memTotal)}`
    : undefined
  const diskTotal = host.disk_total || disk?.latest?.total
  const diskSub = diskTotal
    ? `${disk?.latest?.used != null ? formatBytes(disk.latest.used) : '--'} / ${formatBytes(diskTotal)}`
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

function MetaCell({
  label,
  value,
  mono = true,
  className,
}: {
  label: string
  value: string
  mono?: boolean
  className?: string
}) {
  return (
    <div className={cn('min-w-0', className)}>
      <div className="text-[9px] uppercase tracking-wider text-muted-foreground/80">{label}</div>
      <div className={cn('truncate text-[11px] leading-tight', mono && 'font-mono')} title={value}>
        {value}
      </div>
    </div>
  )
}

/** Full-card placeholder shown while a card's queries do their first load, so
 *  the card appears whole instead of fields/charts/backups popping in one by
 *  one. Mirrors the real card's frame to avoid layout shift. */
function HostCardSkeleton() {
  return (
    <div className="relative">
      <div className="cyber-frame relative flex flex-col overflow-hidden rounded-xl border border-border/60 bg-card backdrop-blur-xl dark:border-white/10">
        <div className="absolute left-0 right-0 top-0 z-3 h-[3px] bg-muted-foreground/15" />
        <div className="relative z-2 flex-1 p-3 pt-5">
          <div className="mb-2.5 flex items-start gap-2">
            <Skeleton className="mt-0.5 h-4 w-4 shrink-0 rounded" />
            <div className="min-w-0 flex-1 space-y-1.5">
              <Skeleton className="h-3.5 w-28" />
              <Skeleton className="h-2.5 w-20" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-1.5">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-16 w-full rounded-md" />
            ))}
          </div>
          <div className="mt-2.5 space-y-1.5">
            <Skeleton className="h-3 w-44" />
            <div className="grid grid-cols-2 gap-x-3 gap-y-1.5 pt-0.5">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-3 w-20" />
            </div>
          </div>
        </div>
        <div className="relative z-2 flex items-center gap-4 border-t border-border/60 bg-muted/25 px-3 py-2 dark:border-white/8 dark:bg-black/20">
          <Skeleton className="h-3 w-12" />
          <Skeleton className="h-3 w-14" />
        </div>
      </div>
    </div>
  )
}

function HostCard({ host, guests = [], live }: { host: Host; guests?: Host[]; live: boolean }) {
  // A card is one consistent entity: gather every query it needs (metrics,
  // health, PBS backups) and hold a skeleton until they've all loaded once, so
  // the card pops in whole instead of charts/fields/backups arriving piecemeal.
  const opts = live ? SSE_LIVE : SSE_FALLBACK
  const cpu = useCPU(host.id, opts)
  const mem = useMemory(host.id, opts)
  const disk = useDisk(host.id, opts)
  const net = useNetwork(host.id, opts)
  const conn = useConnectionStatus(host.id)
  const isPBS = host.platform === 'proxmox-backup-server'
  const pbs = usePBS(host.id, isPBS)

  if (
    cpu.isLoading ||
    mem.isLoading ||
    disk.isLoading ||
    net.isLoading ||
    conn.isLoading ||
    (isPBS && pbs.isLoading)
  ) {
    return <HostCardSkeleton />
  }

  const { isConnected, latency, uptime, showUptime } = conn
  const cardTitle = getHostCardTitle(host)
  // Static identity comes from the host row (one /hosts request); fall back to
  // the cpu metric only if the row hasn't been enriched yet.
  const cpuModel = host.cpu_model?.trim() || cpu.data?.latest?.model_name?.trim()

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
              <div className="flex min-w-0 items-start gap-2">
                <OSIcon host={host} className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0">
                  {cardTitle && (
                    <h2 className="flex items-center gap-1.5 truncate font-display text-sm font-semibold leading-tight tracking-wide transition-colors duration-200 group-hover:text-primary">
                      <span className="truncate">{cardTitle}</span>
                      {host.origin_cluster && (
                        <Badge
                          variant="secondary"
                          className="h-4 shrink-0 border border-violet-500/40 bg-violet-500/10 px-1 font-mono text-[9px] uppercase text-violet-500 dark:text-violet-300"
                          title={`Uplinked from the "${host.origin_cluster}" cluster`}
                        >
                          {host.origin_cluster}
                        </Badge>
                      )}
                    </h2>
                  )}
                  {(host.platform || host.os) && (
                    <p className="truncate font-mono text-[10px] text-muted-foreground">
                      {host.platform || host.os}
                      {host.platform_version ? ` ${host.platform_version}` : ''}
                    </p>
                  )}
                </div>
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
            <HostMetrics host={host} live={live} />

            {/* Compact metadata */}
            <div className="mt-2.5 grid grid-cols-2 gap-x-3 gap-y-1.5">
              {cpuModel && <MetaCell label="CPU" value={cpuModel} className="col-span-2" />}
              {host.ipv4 && <MetaCell label="IPv4" value={host.ipv4} />}
              {host.virtualization_system && <MetaCell label="Virt" value={host.virtualization_system} />}
              {host.kernel_version && <MetaCell label="Kernel" value={host.kernel_version} />}
              {isRealMac(host.mac_address) && <MetaCell label="MAC" value={host.mac_address} />}
            </div>

            {/* Nested guests (hypervisor cards) */}
            <GuestList guests={guests} compact className="mt-2.5" />

            {/* PBS backup summary — last backup time + size per machine */}
            {host.platform === 'proxmox-backup-server' && (
              <PBSCardSummary hostId={host.id} className="mt-2.5" />
            )}
          </div>

          {/* Footer */}
          <div
            className={cn(
              'relative z-2 flex items-center gap-4 border-t px-3 py-2 text-xs text-muted-foreground',
              'border-border/60 bg-muted/25 backdrop-blur-md dark:border-white/8 dark:bg-black/20'
            )}
          >
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
          </div>
        </div>
      </div>
    </Link>
  )
}

export function MachineListPage() {
  const { data: hostsData, isLoading } = useHosts()
  const hosts: Host[] = hostsData?.hosts ?? []
  // One SSE subscription feeds every host's gauges AND metric caches; cards
  // update live from it. Only when SSE is down do cards fall back to a slow poll.
  const { connected: sseConnected } = useHostGaugesStream()

  // Topology grouping: guests (parent resolved) nest inside their hypervisor's
  // card and leave the top-level grid — the no-duplication rule.
  const knownIds = new Set(hosts.map((h) => h.id))
  const guestsByParent = new Map<number, Host[]>()
  for (const h of hosts) {
    if (h.parent_id && h.parent_id !== h.id && knownIds.has(h.parent_id)) {
      guestsByParent.set(h.parent_id, [...(guestsByParent.get(h.parent_id) ?? []), h])
    }
  }
  const topLevel = hosts.filter((h) => !(h.parent_id && h.parent_id !== h.id && knownIds.has(h.parent_id)))

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
              {topLevel.length}
            </Badge>
          )}
        </div>

        {isLoading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {[1, 2, 3, 4].map((i) => (
              <HostCardSkeleton key={i} />
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
            {topLevel.map((host) => (
              <HostCard key={host.id} host={host} guests={guestsByParent.get(host.id) ?? []} live={sseConnected} />
            ))}
          </div>
        )}
      </section>

      <AllApplicationsSection />
    </div>
  )
}
