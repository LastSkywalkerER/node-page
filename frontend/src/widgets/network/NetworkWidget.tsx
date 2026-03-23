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
  const primary = ifaces.find((i: any) => i.is_primary)
  return primary ?? ifaces.reduce((b, c) => totalTraffic(c) > totalTraffic(b) ? c : b)
}

const chartConfig: ChartConfig = {
  speed: { label: 'Upload MB/s', color: CHART_COLORS.network },
}

export function NetworkWidget({ hostId }: NetworkWidgetProps) {
  const { data: metrics, isLoading } = useNetwork(hostId)
  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={Network} label="Network" />

  const ifaces: NetworkInterface[] = metrics.latest?.interfaces ?? []
  const primary = getPrimary(ifaces)
  const active = ifaces.filter(i => isActiveIface(i) && i.name !== primary?.name)
  const inactive = ifaces.filter(i => !isActiveIface(i) && i.name !== primary?.name)

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Network className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">Network</span>
          </div>
          {primary && (
            <span className="text-xs font-mono text-muted-foreground">{primary.name}</span>
          )}
        </div>
        {primary && (
          <div className="mt-2 flex items-center gap-3 text-sm">
            <span className="flex items-center gap-1 font-medium tabular-nums" style={{ color: CHART_COLORS.network }}>
              <ArrowUp className="h-3 w-3" />{fmtSpeed(primary.speed_kbps_sent)}
            </span>
            <span className="flex items-center gap-1 font-medium tabular-nums text-muted-foreground">
              <ArrowDown className="h-3 w-3" />{fmtSpeed(primary.speed_kbps_recv)}
            </span>
          </div>
        )}
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <div className="space-y-1 text-xs">
          {primary && (
            <>
              {(primary as any).ips?.length > 0 && (
                <div className="flex justify-between">
                  <span className="text-muted-foreground">IP</span>
                  <span className="font-mono font-medium">{(primary as any).ips.find((ip: string) => ip.includes('.')) ?? (primary as any).ips[0]}</span>
                </div>
              )}
              <div className="flex justify-between">
                <span className="text-muted-foreground">Total sent</span>
                <span className="font-medium">{fmtBytes(primary.bytes_sent)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Total recv</span>
                <span className="font-medium">{fmtBytes(primary.bytes_recv)}</span>
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
        {metrics.history && metrics.history.length > 0 && primary && (
          <ChartContainer config={chartConfig} className="h-20 w-full">
            <AreaChart data={metrics.history.slice(-20).map((p: any) => {
              const iface = p.interfaces?.find((i: any) => i.name === primary.name)
              const d = new Date(p.timestamp)
              return {
                time: isNaN(d.getTime()) ? '' : format(d, 'HH:mm'),
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
