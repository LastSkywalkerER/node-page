import { useMemo, useState } from 'react'
import { format } from 'date-fns'
import { toast } from 'sonner'
import { Activity, Ban, ShieldOff, Trash2, RefreshCw } from 'lucide-react'
import { Area, AreaChart, XAxis, YAxis } from 'recharts'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { CHART_COLORS } from '@/shared/lib/chartColors'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { cn } from '@/lib/utils'
import {
  useGatewayConnections,
  useGatewayBlocks,
  useCreateBlock,
  useDeleteBlock,
} from './useGateway'
import type { ConnIP, ConnEvent, GatewayBlock, GatewayState } from './schemas'

const tsFmt = (ts: number, f = 'HH:mm:ss') => {
  const d = new Date(ts * 1000)
  return isNaN(d.getTime()) ? '' : format(d, f)
}

function statusCls(s: number): string {
  if (s >= 500) return 'text-red-400'
  if (s >= 400) return 'text-amber-400'
  if (s >= 300) return 'text-sky-400'
  return 'text-emerald-400'
}

function suspicionBadge(score: number) {
  if (score >= 60) return <Badge className="bg-red-500/15 text-red-400 border-red-500/30">sus {score}</Badge>
  if (score >= 30) return <Badge className="bg-amber-500/15 text-amber-400 border-amber-500/30">sus {score}</Badge>
  return null
}

// ---------------------------------------------------------------------------
// Connections (live stats from the gateway node's access log — RAM only)
// ---------------------------------------------------------------------------

export function ConnectionsCard({ state }: { state: GatewayState }) {
  const enabled = state.config.enabled
  const { data, isFetching, refetch } = useGatewayConnections(enabled)
  const createBlock = useCreateBlock()
  const [tab, setTab] = useState<'recent' | 'top'>('recent')

  const chartConfig: ChartConfig = {
    total: { label: 'requests/min', color: CHART_COLORS.network ?? '#22d3ee' },
    e4xx: { label: '4xx', color: '#f59e0b' },
  }
  const chartData = useMemo(
    () =>
      (data?.minutes ?? []).slice(-60).map((p) => ({
        time: tsFmt(p.ts * 60, 'HH:mm'),
        total: p.total,
        e4xx: p.e4xx + p.e5xx,
      })),
    [data?.minutes]
  )

  if (!enabled) return null

  const onBlock = async (ip: string, reason: string) => {
    const { confirmed, checked } = await confirmDialog({
      title: `Block ${ip}?`,
      description:
        'The gateway will answer 403 to this address on every route within a second. You can lift the block below at any time.',
      variant: 'destructive',
      confirmText: 'Block',
      checkbox: { label: 'Block the whole /24 (neighbouring addresses too)' },
    })
    if (!confirmed) return
    const cidr = checked ? ip.split('.').slice(0, 3).join('.') + '.0/24' : ip
    createBlock.mutate(
      { cidr, reason, ttl_hours: 0 },
      {
        onSuccess: () => toast.success(`Blocked ${cidr}`),
        onError: (e) => toast.error(e.message),
      }
    )
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Activity className="h-4 w-4" /> Connections
            </CardTitle>
            <CardDescription>
              Live requests hitting the gateway, aggregated in memory on the gateway node
              {data?.available && data.since_ts ? ` — since ${tsFmt(data.since_ts, 'HH:mm dd.MM')}` : ''}. Refreshes
              every 5 s.
            </CardDescription>
          </div>
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => refetch()} title="Refresh">
            <RefreshCw className={cn('h-4 w-4', isFetching && 'animate-spin')} />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {!data?.available ? (
          <p className="text-sm text-muted-foreground">{data?.reason || 'loading…'}</p>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat label="requests" value={data.total} />
              <Stat label="unique IPs" value={data.unique_ips} />
              <Stat label="no route (scans)" value={data.no_route} warn={data.no_route > 0} />
              <Stat label="blocked hits" value={data.blocked_total} warn={data.blocked_total > 0} />
            </div>

            {chartData.some((p) => p.total > 0) && (
              <ChartContainer config={chartConfig} className="h-24 w-full">
                <AreaChart data={chartData} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
                  <defs>
                    <linearGradient id="gwConnGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="var(--color-total)" stopOpacity={0.25} />
                      <stop offset="95%" stopColor="var(--color-total)" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis dataKey="time" axisLine={false} tickLine={false} tick={{ fontSize: 9 }} minTickGap={40} />
                  <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 9 }} width={28} allowDecimals={false} />
                  <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel />} />
                  <Area dataKey="total" stroke="var(--color-total)" fill="url(#gwConnGrad)" strokeWidth={1.5} dot={false} />
                  <Area dataKey="e4xx" stroke="var(--color-e4xx)" fill="none" strokeWidth={1} dot={false} />
                </AreaChart>
              </ChartContainer>
            )}

            <div className="flex gap-1">
              {(['recent', 'top'] as const).map((t) => (
                <Button key={t} size="sm" variant={tab === t ? 'secondary' : 'ghost'} onClick={() => setTab(t)}>
                  {t === 'recent' ? 'Live feed' : 'Top clients'}
                </Button>
              ))}
            </div>

            {tab === 'recent' ? (
              <RecentTable rows={data.recent} />
            ) : (
              <TopTable rows={data.top} onBlock={onBlock} />
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

function Stat({ label, value, warn }: { label: string; value: number; warn?: boolean }) {
  return (
    <div className="rounded-lg border border-border/60 px-3 py-2">
      <div className={cn('font-mono text-lg leading-tight', warn ? 'text-amber-400' : '')}>{value}</div>
      <div className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</div>
    </div>
  )
}

function RecentTable({ rows }: { rows: ConnEvent[] }) {
  if (!rows.length) return <p className="text-sm text-muted-foreground">No requests seen yet.</p>
  return (
    <div className="max-h-80 overflow-auto rounded-lg border border-border/60">
      <table className="w-full text-left font-mono text-[11px]">
        <tbody>
          {rows.map((e, i) => (
            <tr key={i} className="border-b border-border/40 last:border-0">
              <td className="whitespace-nowrap px-2 py-1 text-muted-foreground">{tsFmt(e.ts)}</td>
              <td className="whitespace-nowrap px-2 py-1">{e.ip}</td>
              <td className="max-w-40 truncate px-2 py-1 text-muted-foreground" title={e.host}>{e.host}</td>
              <td className="max-w-56 truncate px-2 py-1" title={`${e.method} ${e.path}`}>
                {e.method} {e.path}
              </td>
              <td className={cn('whitespace-nowrap px-2 py-1', statusCls(e.status))}>
                {e.blocked ? 'BLOCKED' : e.status}
              </td>
              <td className="whitespace-nowrap px-2 py-1 text-muted-foreground">{e.dur_ms}ms</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function TopTable({ rows, onBlock }: { rows: ConnIP[]; onBlock: (ip: string, reason: string) => void }) {
  if (!rows.length) return <p className="text-sm text-muted-foreground">No clients seen yet.</p>
  return (
    <div className="max-h-80 overflow-auto rounded-lg border border-border/60">
      <table className="w-full text-left text-[11px]">
        <thead className="sticky top-0 bg-background/95 text-[10px] uppercase tracking-wide text-muted-foreground">
          <tr>
            <th className="px-2 py-1.5">client</th>
            <th className="px-2 py-1.5">req</th>
            <th className="px-2 py-1.5">4xx/5xx</th>
            <th className="px-2 py-1.5">no route</th>
            <th className="px-2 py-1.5">last</th>
            <th className="px-2 py-1.5" />
          </tr>
        </thead>
        <tbody className="font-mono">
          {rows.map((r) => (
            <tr key={r.ip} className="border-b border-border/40 last:border-0">
              <td className="whitespace-nowrap px-2 py-1">
                <span className="flex items-center gap-1.5">
                  {r.ip}
                  {suspicionBadge(r.suspicion)}
                  {r.is_blocked && <Badge variant="outline" className="text-red-400 border-red-500/30">blocked</Badge>}
                </span>
              </td>
              <td className="px-2 py-1">{r.count}</td>
              <td className={cn('px-2 py-1', r.s4xx + r.s5xx > 0 && 'text-amber-400')}>{r.s4xx + r.s5xx}</td>
              <td className={cn('px-2 py-1', r.no_route > 0 && 'text-amber-400')}>{r.no_route}</td>
              <td className="max-w-64 truncate px-2 py-1 text-muted-foreground" title={`${r.last_host}${r.last_path} · ${r.last_ua}`}>
                {tsFmt(r.last_seen)} · {r.last_host}
                {r.last_path}
              </td>
              <td className="px-2 py-1 text-right">
                {!r.is_blocked && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6 text-red-400 hover:text-red-500"
                    title={`Block ${r.ip}`}
                    onClick={() => onBlock(r.ip, r.scanner_hits > 0 ? 'scanner paths' : r.no_route > 0 ? 'no-route probing' : '')}
                  >
                    <Ban className="h-3.5 w-3.5" />
                  </Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Blocked clients (replicated deny list)
// ---------------------------------------------------------------------------

export function BlocksCard({ state }: { state: GatewayState }) {
  const enabled = state.config.enabled
  const { data } = useGatewayBlocks(enabled)
  const createBlock = useCreateBlock()
  const deleteBlock = useDeleteBlock()
  const [cidr, setCidr] = useState('')
  const [reason, setReason] = useState('')
  const [ttl, setTtl] = useState(0)

  if (!enabled) return null
  const blocks = data ?? []

  const add = () => {
    if (!cidr.trim()) return
    createBlock.mutate(
      { cidr: cidr.trim(), reason: reason.trim(), ttl_hours: ttl },
      {
        onSuccess: (b) => {
          toast.success(`Blocked ${b.cidr}`)
          setCidr('')
          setReason('')
        },
        onError: (e) => toast.error(e.message),
      }
    )
  }

  const remove = async (b: GatewayBlock) => {
    const { confirmed } = await confirmDialog({
      title: `Unblock ${b.cidr}?`,
      description: 'The gateway will accept requests from this range again within a second.',
      confirmText: 'Unblock',
    })
    if (!confirmed) return
    deleteBlock.mutate(b.block_id, {
      onSuccess: () => toast.success(`Unblocked ${b.cidr}`),
      onError: (e) => toast.error(e.message),
    })
  }

  const expired = (b: GatewayBlock) => !!b.expires_at && new Date(b.expires_at).getTime() < Date.now()

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldOff className="h-4 w-4" /> Blocked clients
        </CardTitle>
        <CardDescription>
          Denied IPs/CIDRs — rejected with 403 above every route. Replicated across the cluster; applied on the
          gateway node within a second.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            className="h-8 w-44 font-mono text-xs"
            placeholder="203.0.113.7 or …/24"
            value={cidr}
            onChange={(e) => setCidr(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && add()}
          />
          <Input
            className="h-8 w-48 text-xs"
            placeholder="reason (optional)"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && add()}
          />
          <select
            className="flex h-8 rounded-md border border-input bg-background px-2 text-xs shadow-xs outline-none"
            value={ttl}
            onChange={(e) => setTtl(Number(e.target.value))}
          >
            <option value={0}>permanent</option>
            <option value={1}>1 hour</option>
            <option value={24}>24 hours</option>
            <option value={168}>7 days</option>
          </select>
          <Button size="sm" onClick={add} disabled={createBlock.isPending || !cidr.trim()}>
            <Ban className="mr-1 h-3.5 w-3.5" /> Block
          </Button>
        </div>

        {blocks.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing blocked.</p>
        ) : (
          <div className="divide-y divide-border/40 rounded-lg border border-border/60">
            {blocks.map((b) => (
              <div key={b.block_id} className={cn('flex items-center gap-3 px-3 py-2 text-xs', expired(b) && 'opacity-50')}>
                <span className="font-mono">{b.cidr}</span>
                {b.reason && <span className="truncate text-muted-foreground">{b.reason}</span>}
                <span className="ml-auto whitespace-nowrap text-muted-foreground">
                  {expired(b)
                    ? 'expired'
                    : b.expires_at
                      ? `until ${format(new Date(b.expires_at), 'HH:mm dd.MM')}`
                      : 'permanent'}
                  {b.created_by ? ` · ${b.created_by}` : ''}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6 text-muted-foreground hover:text-red-400"
                  title="Unblock"
                  onClick={() => remove(b)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
