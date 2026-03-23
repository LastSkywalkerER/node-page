import { AreaChart, Area, XAxis, YAxis } from 'recharts'
import { Cpu } from 'lucide-react'
import { format } from 'date-fns'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { useCPU } from './useCPU'

interface CPUWidgetProps { hostId: number }

const SHOW_KEYS = ['cores', 'model_name', 'mhz', 'temperature', 'load_avg_1', 'load_avg_5', 'load_avg_15', 'cache_size']
const LABEL_MAP: Record<string, string> = {
  cores: 'Cores', model_name: 'Model', vendor_id: 'Vendor', mhz: 'Clock',
  temperature: 'Temp', load_avg_1: 'Load 1m', load_avg_5: 'Load 5m',
  load_avg_15: 'Load 15m', cache_size: 'Cache',
}

function fmt(key: string, value: unknown): string {
  if (value == null) return 'N/A'
  if (key === 'mhz' && typeof value === 'number') return `${value.toFixed(0)} MHz`
  if (key === 'temperature' && typeof value === 'number') return `${value.toFixed(1)}°C`
  if (key.startsWith('load_avg_') && typeof value === 'number') return value.toFixed(2)
  if (Array.isArray(value)) return value.join(', ')
  return String(value)
}

function usageColor(pct: number) {
  if (pct > 90) return '#ef4444'
  if (pct > 70) return '#f59e0b'
  return CHART_COLORS.cpu
}

export function CPUWidget({ hostId }: CPUWidgetProps) {
  const { data: metrics, isLoading } = useCPU(hostId)
  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={Cpu} label="CPU" />

  const pct = metrics.latest?.usage_percent ?? 0
  const color = usageColor(pct)
  const latest = metrics.latest ?? {} as Record<string, unknown>
  const details = SHOW_KEYS
    .filter(k => latest[k] != null && latest[k] !== 0 && latest[k] !== '')
    .map(k => ({ key: k, label: LABEL_MAP[k] ?? k, value: fmt(k, latest[k]) }))

  const chartConfig: ChartConfig = { usage: { label: 'CPU %', color } }

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Cpu className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">CPU</span>
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
                <span className="truncate max-w-[55%] text-right font-medium">{e.value}</span>
              </div>
            ))}
          </div>
        )}
        {metrics.history && metrics.history.length > 0 && (
          <ChartContainer config={chartConfig} className="h-20 w-full">
            <AreaChart data={metrics.history.map((p: any) => {
              const d = new Date(p.timestamp)
              return { time: isNaN(d.getTime()) ? '' : format(d, 'HH:mm'), usage: p.usage }
            })} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="cpuGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-usage)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-usage)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="time" axisLine={false} tickLine={false} tick={{ fontSize: 9 }} interval="preserveStartEnd" />
              <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 9 }} domain={[0, 100]} width={24} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel />} />
              <Area type="monotone" dataKey="usage" stroke="var(--color-usage)" fill="url(#cpuGrad)" strokeWidth={1.5} dot={false} />
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  )
}
