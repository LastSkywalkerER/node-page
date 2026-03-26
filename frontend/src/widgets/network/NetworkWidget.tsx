import { useEffect, useMemo, useState } from 'react'
import { AreaChart, Area, XAxis, YAxis } from 'recharts'
import { Network, ArrowUp, ArrowDown } from 'lucide-react'
import { format } from 'date-fns'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { useNetwork } from './useNetwork'
import { NetworkInterface } from './schemas'

interface NetworkWidgetProps { hostId: number }

const fmtBytes = (b: number) => b >= 1e9 ? `${(b/1e9).toFixed(1)} GB` : b >= 1e6 ? `${(b/1e6).toFixed(1)} MB` : b >= 1e3 ? `${(b/1e3).toFixed(1)} KB` : `${b} B`
const fmtSpeed = (kbps: number) => kbps >= 1e6 ? `${(kbps/1e6).toFixed(1)} GB/s` : kbps >= 1024 ? `${(kbps/1024).toFixed(1)} MB/s` : `${kbps.toFixed(0)} KB/s`
const totalTraffic = (i: NetworkInterface) => i.bytes_sent + i.bytes_recv
const isActiveIface = (i: NetworkInterface) => i.speed_kbps_sent > 0 || i.speed_kbps_recv > 0

function getPrimary(ifaces: NetworkInterface[]) {
  if (!ifaces.length) return null
  const primary = ifaces.find((i) => i.is_primary)
  return primary ?? ifaces.reduce((b, c) => totalTraffic(c) > totalTraffic(b) ? c : b)
}

const chartConfig: ChartConfig = {
  speed: { label: 'Upload MB/s', color: CHART_COLORS.network },
}

export function NetworkWidget({ hostId }: NetworkWidgetProps) {
  const { data: metrics, isLoading } = useNetwork(hostId)
  const [ifacePick, setIfacePick] = useState<string | null>(null)

  const ifaces: NetworkInterface[] = metrics?.latest?.interfaces ?? []
  const primaryDefault = getPrimary(ifaces)

  const selected = useMemo(() => {
    if (!ifaces.length) return null
    if (ifacePick && ifaces.some((i) => i.name === ifacePick)) {
      return ifaces.find((i) => i.name === ifacePick) ?? null
    }
    return primaryDefault
  }, [ifaces, ifacePick, primaryDefault])

  useEffect(() => {
    if (ifacePick && !ifaces.some((i) => i.name === ifacePick)) {
      setIfacePick(null)
    }
  }, [ifaces, ifacePick])

  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={Network} label="Network" />

  const active = ifaces.filter(i => isActiveIface(i) && i.name !== selected?.name)
  const inactive = ifaces.filter(i => !isActiveIface(i) && i.name !== selected?.name)

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2 min-w-0">
            <Network className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">Network</span>
          </div>
          {ifaces.length > 0 && (
            <label className="flex items-center gap-1.5 min-w-0 shrink">
              <span className="sr-only">Interface</span>
              <select
                className="max-w-36 truncate rounded-md border border-input bg-transparent py-1 pr-6 pl-2 text-xs font-mono text-muted-foreground outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40 dark:bg-input/30"
                value={selected?.name ?? ''}
                onChange={(e) => setIfacePick(e.target.value || null)}
                aria-label="Network interface"
              >
                {ifaces.map((i) => (
                  <option key={i.name} value={i.name}>
                    {i.name}
                    {i.is_primary ? ' (default)' : ''}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        {selected && (
          <div className="mt-2 flex items-center gap-3 text-sm">
            <span className="flex items-center gap-1 font-medium tabular-nums" style={{ color: CHART_COLORS.network }}>
              <ArrowUp className="h-3 w-3" />{fmtSpeed(selected.speed_kbps_sent)}
            </span>
            <span className="flex items-center gap-1 font-medium tabular-nums text-muted-foreground">
              <ArrowDown className="h-3 w-3" />{fmtSpeed(selected.speed_kbps_recv)}
            </span>
          </div>
        )}
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <div className="space-y-1 text-xs">
          {selected && (
            <>
              {selected.ips.length > 0 && (
                <div className="flex justify-between gap-2">
                  <span className="text-muted-foreground shrink-0">IP</span>
                  <span className="font-mono font-medium text-right truncate">{selected.ips.find((ip) => ip.includes('.')) ?? selected.ips[0]}</span>
                </div>
              )}
              <div className="flex justify-between">
                <span className="text-muted-foreground">MAC</span>
                <span className="font-mono font-medium truncate max-w-[60%] text-right">{selected.mac || '—'}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Total sent</span>
                <span className="font-medium">{fmtBytes(selected.bytes_sent)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Total recv</span>
                <span className="font-medium">{fmtBytes(selected.bytes_recv)}</span>
              </div>
            </>
          )}
          {active.map((i: NetworkInterface) => (
            <div key={i.name} className="flex justify-between">
              <span className="text-muted-foreground">{i.name}</span>
              <span>↑{fmtSpeed(i.speed_kbps_sent)} ↓{fmtSpeed(i.speed_kbps_recv)}</span>
            </div>
          ))}
          {inactive.length > 0 && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Inactive</span>
              <span className="truncate max-w-[55%] text-right">{inactive.map(i => i.name).join(', ')}</span>
            </div>
          )}
        </div>
        {metrics.history && metrics.history.length > 0 && selected && (
          <ChartContainer config={chartConfig} className="h-20 w-full">
            <AreaChart data={metrics.history.slice(-20).map((p) => {
              const iface = p.interfaces?.find((i) => i.name === selected.name)
              const d = new Date(p.timestamp)
              return {
                time: isNaN(d.getTime()) ? '' : format(d, 'HH:mm:ss'),
                speed: iface ? iface.speed_kbps_sent / 1024 : 0,
              }
            })} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="netGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-speed)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-speed)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="time" axisLine={false} tickLine={false} tick={{ fontSize: 9 }} interval="preserveStartEnd" />
              <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 9 }} width={28} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel formatter={(v) => `${Number(v).toFixed(2)} MB/s`} />} />
              <Area type="monotone" dataKey="speed" stroke="var(--color-speed)" fill="url(#netGrad)" strokeWidth={1.5} dot={false} />
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  )
}
