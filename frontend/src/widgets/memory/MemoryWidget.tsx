import { AreaChart, Area, XAxis, YAxis } from 'recharts'
import { MemoryStick } from 'lucide-react'
import { format } from 'date-fns'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { formatBytes } from '@/shared/lib/utils'
import { useMemory } from './useMemory'

interface MemoryWidgetProps { hostId: number }

const SHOW_KEYS = ['total', 'used', 'available', 'cached', 'buffers', 'swap_total', 'swap_used']
const LABEL_MAP: Record<string, string> = {
  total: 'Total', available: 'Available', used: 'Used', free: 'Free',
  cached: 'Cached', buffers: 'Buffers', swap_total: 'Swap Total',
  swap_used: 'Swap Used', swap_free: 'Swap Free',
}
const BYTES_KEYS = new Set(['total', 'available', 'used', 'free', 'cached', 'buffers',
  'active', 'inactive', 'shared', 'swap_total', 'swap_used', 'swap_free'])

function usageColor(pct: number) {
  if (pct > 90) return '#ef4444'
  if (pct > 75) return '#f59e0b'
  return CHART_COLORS.memory
}

export function MemoryWidget({ hostId }: MemoryWidgetProps) {
  const { data: metrics, isLoading } = useMemory(hostId)
  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={MemoryStick} label="Memory" />

  const pct = metrics.latest?.usage_percent ?? 0
  const color = usageColor(pct)
  const latest = metrics.latest as Record<string, unknown>
  const details = SHOW_KEYS
    .filter(k => latest[k] != null && latest[k] !== 0)
    .map(k => ({
      key: k,
      label: LABEL_MAP[k] ?? k.replace(/_/g, ' '),
      value: BYTES_KEYS.has(k) && typeof latest[k] === 'number' ? formatBytes(latest[k] as number) : String(latest[k]),
    }))

  const chartConfig: ChartConfig = { used: { label: 'Used', color } }

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <MemoryStick className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">Memory</span>
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
        {details.length > 0 && (
          <div className="space-y-1">
            {details.map(e => (
              <div key={e.key} className="flex justify-between text-xs">
                <span className="text-muted-foreground">{e.label}</span>
                <span className="font-medium">{e.value}</span>
              </div>
            ))}
          </div>
        )}
        {metrics.history && metrics.history.length > 0 && (
          <ChartContainer config={chartConfig} className="h-20 w-full">
            <AreaChart data={metrics.history.map((p) => {
              const d = new Date(p.timestamp)
              return { time: isNaN(d.getTime()) ? '' : format(d, 'HH:mm'), used: p.used_bytes }
            })} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="memGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-used)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-used)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="time" axisLine={false} tickLine={false} tick={{ fontSize: 9 }} interval="preserveStartEnd" />
              <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 9 }} tickFormatter={v => formatBytes(v)} width={36} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel formatter={(v) => formatBytes(Number(v))} />} />
              <Area type="monotone" dataKey="used" stroke="var(--color-used)" fill="url(#memGrad)" strokeWidth={1.5} dot={false} />
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  )
}
