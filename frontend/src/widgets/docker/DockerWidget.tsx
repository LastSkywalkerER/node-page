import { useState } from 'react'
import { toast } from 'sonner'
import { Server, ChevronDown, ChevronRight, MoreHorizontal, RotateCcw, Square, Trash2, Play } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { getContainerStateColor } from '@/shared/lib/utils'
import { useDocker } from './useDocker'

interface DockerWidgetProps { hostId: number }

const fmtBytes = (b: number) =>
  b >= 1e9 ? `${(b/1e9).toFixed(1)}G` : b >= 1e6 ? `${(b/1e6).toFixed(1)}M` : b >= 1e3 ? `${(b/1e3).toFixed(0)}K` : `${b}B`

function fmtUptime(created: string) {
  const ms = Date.now() - new Date(created).getTime()
  if (isNaN(ms) || ms < 0) return '?'
  const d = Math.floor(ms / 86400000)
  const h = Math.floor((ms % 86400000) / 3600000)
  const m = Math.floor((ms % 3600000) / 60000)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

function fmtPorts(ports: any[]): string {
  if (!ports?.length) return ''
  return ports
    .filter((p: any) => p.public_port)
    .map((p: any) => `:${p.public_port}→${p.private_port}`)
    .slice(0, 3)
    .join(' ')
}

function metricColor(pct: number) {
  if (pct >= 90) return '#ef4444'
  if (pct >= 75) return '#f59e0b'
  return '#6b7280'
}

function RingGauge({ value, color, size = 20 }: { value: number; color: string; size?: number }) {
  const cx = size / 2
  const r = (size - 5) / 2
  const circ = 2 * Math.PI * r
  const dash = Math.min(value / 100, 1) * circ
  return (
    <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }} className="shrink-0">
      <circle cx={cx} cy={cx} r={r} fill="none" stroke="currentColor" strokeWidth={2.5} className="text-muted" />
      <circle cx={cx} cy={cx} r={r} fill="none" stroke={color} strokeWidth={2.5}
        strokeDasharray={`${dash} ${circ}`} strokeLinecap="round" />
    </svg>
  )
}

const notImplemented = (action: string, name: string) =>
  toast.info(`${action} — not implemented`, {
    description: `Container actions for "${name}" are not yet available.`,
    duration: 3000,
  })

function ContainerActions({ name, state }: { name: string; state: string }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="p-0.5 rounded hover:bg-muted transition-colors text-muted-foreground/60 hover:text-foreground shrink-0">
        <MoreHorizontal className="h-3.5 w-3.5" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-36 text-xs">
        {state !== 'running' && (
          <DropdownMenuItem className="gap-2 text-xs" onClick={() => notImplemented('Start', name)}>
            <Play className="h-3 w-3" /> Start
          </DropdownMenuItem>
        )}
        <DropdownMenuItem className="gap-2 text-xs" onClick={() => notImplemented('Restart', name)}>
          <RotateCcw className="h-3 w-3" /> Restart
        </DropdownMenuItem>
        {state === 'running' && (
          <DropdownMenuItem className="gap-2 text-xs" onClick={() => notImplemented('Stop', name)}>
            <Square className="h-3 w-3" /> Stop
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem className="gap-2 text-xs text-destructive focus:text-destructive" onClick={() => notImplemented('Remove', name)}>
          <Trash2 className="h-3 w-3" /> Remove
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function DockerWidget({ hostId }: DockerWidgetProps) {
  const { data: metrics, isLoading } = useDocker(hostId)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={Server} label="Docker" />

  const latest = metrics.latest
  const running = latest?.running_containers ?? 0
  const total = latest?.total_containers ?? 0
  const stacks: any[] = latest?.stacks ?? []

  const toggle = (name: string) => setExpanded(prev => {
    const next = new Set(prev)
    next.has(name) ? next.delete(name) : next.add(name)
    return next
  })

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Server className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">Docker</span>
          </div>
          <div className="flex items-center gap-2 text-xs">
            <span className="text-green-500 font-medium">{running} running</span>
            <span className="text-muted-foreground">/ {total} total</span>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-1.5 pt-0">
        {stacks.length === 0 ? (
          <p className="text-xs text-muted-foreground">No containers</p>
        ) : stacks.map((stack: any) => (
          <div key={stack.name} className="rounded-md border border-border overflow-hidden">
            <button
              className="w-full flex items-center justify-between px-2.5 py-2 text-left hover:bg-muted/40 transition-colors"
              onClick={() => toggle(stack.name)}
            >
              <div className="flex items-center gap-2">
                {expanded.has(stack.name)
                  ? <ChevronDown className="h-3 w-3 text-muted-foreground" />
                  : <ChevronRight className="h-3 w-3 text-muted-foreground" />
                }
                <span className="text-xs font-semibold">{stack.name}</span>
                <Badge variant="secondary" className="text-[10px] px-1.5 py-0 h-4">{stack.total_containers}</Badge>
              </div>
              <span className="text-[10px] text-green-500">{stack.running_containers} running</span>
            </button>

            {expanded.has(stack.name) && (
              <div className="border-t border-border divide-y divide-border/60">
                {stack.containers.map((c: any) => {
                  const cpu = c.stats.cpu_percent_of_limit ?? 0
                  const mem = c.stats.memory_percent ?? 0
                  const cpuColor = metricColor(cpu)
                  const memColor = metricColor(mem)
                  const ports = fmtPorts(c.ports)
                  const uptime = fmtUptime(c.created)
                  const image = c.image?.split('/').pop() ?? c.image ?? ''

                  return (
                    <div key={c.id} className="px-2.5 py-1.5 space-y-0.5">
                      {/* Main row */}
                      <div className="flex items-center gap-2 text-xs min-w-0">
                        <span className="w-1.5 h-1.5 rounded-full shrink-0"
                          style={{ backgroundColor: getContainerStateColor(c.state) }} />
                        <span className="font-medium truncate flex-1 min-w-0">{c.name}</span>

                        <div className="flex items-center gap-1 shrink-0">
                          <RingGauge value={cpu} color={cpuColor} />
                          <span className="tabular-nums w-7 text-right" style={{ color: cpuColor }}>{cpu.toFixed(0)}%</span>
                          <span className="text-muted-foreground/50 text-[10px]">cpu</span>
                        </div>

                        <div className="flex items-center gap-1 shrink-0">
                          <RingGauge value={mem} color={memColor} />
                          <span className="tabular-nums w-7 text-right" style={{ color: memColor }}>{mem.toFixed(0)}%</span>
                          <span className="text-muted-foreground/50 text-[10px]">mem</span>
                        </div>

                        <span className="text-muted-foreground shrink-0 tabular-nums text-[10px]">
                          ↓{fmtBytes(c.stats.network_rx ?? 0)} ↑{fmtBytes(c.stats.network_tx ?? 0)}
                        </span>

                        <ContainerActions name={c.name} state={c.state} />
                      </div>

                      {/* Sub-info row */}
                      <div className="flex items-center gap-2 pl-3.5 text-[10px] text-muted-foreground/50 min-w-0">
                        <span className="truncate max-w-[45%]">{image}</span>
                        {ports && <span className="shrink-0 font-mono">{ports}</span>}
                        <span className="shrink-0">{uptime}</span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
