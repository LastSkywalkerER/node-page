import { AreaChart, Area, XAxis, YAxis } from 'recharts'
import { HardDrive } from 'lucide-react'
import { format } from 'date-fns'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { formatBytes } from '@/shared/lib/utils'
import { useDisk } from './useDisk'
import type { DiskMetric } from './schemas'

interface DiskWidgetProps { hostId: number }

function usageColor(pct: number) {
  if (pct > 90) return '#ef4444'
  if (pct > 75) return '#f59e0b'
  return CHART_COLORS.disk
}

export function DiskWidget({ hostId }: DiskWidgetProps) {
  const { data: metrics, isLoading } = useDisk(hostId)
  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={HardDrive} label="Disk" />

  const pct = metrics.latest?.usage_percent ?? 0
  const color = usageColor(pct)
  const latest = metrics.latest!
  type Mount = DiskMetric['mounts'][number]
  type IOCounter = DiskMetric['io_counters'][number]
  const mounts: Mount[] = Array.isArray(latest.mounts) ? latest.mounts : []
  const ioCounters: IOCounter[] = Array.isArray(latest.io_counters) ? latest.io_counters : []
  const topRead = ioCounters.reduce<IOCounter | null>((b, d) => (b && b.read_bytes > d.read_bytes ? b : d), null)
  const topWrite = ioCounters.reduce<IOCounter | null>((b, d) => (b && b.write_bytes > d.write_bytes ? b : d), null)
  const sortedMounts = mounts.slice().sort((a, b) => b.used_percent - a.used_percent).slice(0, 3)

  const chartConfig: ChartConfig = { used: { label: 'Used', color } }

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <HardDrive className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">Disk</span>
          </div>
          <span className="text-2xl font-bold tabular-nums" style={{ color }}>
            {pct.toFixed(1)}%
          </span>
        </div>
        <div className="mt-2 h-1 rounded-full bg-muted overflow-hidden">
          <div className="h-full rounded-full transition-all duration-500" style={{ width: `${pct}%`, backgroundColor: color }} />
        </div>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <div className="space-y-1 text-xs">
          {latest.total != null && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Total</span>
              <span className="font-medium">{formatBytes(latest.total)}</span>
            </div>
          )}
          {latest.used != null && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Used</span>
              <span className="font-medium">{formatBytes(latest.used)}</span>
            </div>
          )}
          {latest.free != null && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Free</span>
              <span className="font-medium">{formatBytes(latest.free)}</span>
            </div>
          )}
          {sortedMounts.map((m) => {
            const mc = usageColor(m.used_percent)
            return (
              <div key={m.path}>
                <div className="flex justify-between mb-0.5">
                  <span className="text-muted-foreground truncate max-w-[45%]">{m.path}</span>
                  <span style={{ color: mc }}>{m.used_percent.toFixed(1)}% · {formatBytes(m.total)}</span>
                </div>
                <div className="h-0.5 rounded-full bg-muted overflow-hidden">
                  <div className="h-full rounded-full" style={{ width: `${m.used_percent}%`, backgroundColor: mc }} />
                </div>
              </div>
            )
          })}
          {topRead && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Top Read</span>
              <span className="font-medium">{topRead.name} · {formatBytes(topRead.read_bytes)}</span>
            </div>
          )}
          {topWrite && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Top Write</span>
              <span className="font-medium">{topWrite.name} · {formatBytes(topWrite.write_bytes)}</span>
            </div>
          )}
        </div>
        {metrics.history && metrics.history.length > 0 && (
          <ChartContainer config={chartConfig} className="h-20 w-full">
            <AreaChart data={metrics.history.map((p) => {
              const d = new Date(p.timestamp)
              return { time: isNaN(d.getTime()) ? '' : format(d, 'HH:mm:ss'), used: p.used_bytes }
            })} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="diskGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-used)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-used)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="time" axisLine={false} tickLine={false} tick={{ fontSize: 9 }} interval="preserveStartEnd" />
              <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 9 }} tickFormatter={v => formatBytes(v)} width={36} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel formatter={(v) => formatBytes(Number(v))} />} />
              <Area type="monotone" dataKey="used" stroke="var(--color-used)" fill="url(#diskGrad)" strokeWidth={1.5} dot={false} />
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  )
}
