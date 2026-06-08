import { useState } from 'react'
import { Server, ChevronDown, ChevronRight } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { useDocker } from './useDocker'
import { ContainerRow } from './ContainerRow'
import type { DockerStack, DockerContainer } from './schemas'

interface DockerWidgetProps { hostId: number }

export function DockerWidget({ hostId }: DockerWidgetProps) {
  const { data: metrics, isLoading } = useDocker(hostId)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  if (isLoading || !metrics) return <MetricCardSkeleton />
  if (metrics.latest == null) return <MetricWidgetEmpty icon={Server} label="Docker" />

  const latest = metrics.latest
  const running = latest?.running_containers ?? 0
  const total = latest?.total_containers ?? 0
  const stacks: DockerStack[] = latest?.stacks ?? []

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
        {latest.docker_available === false && latest.error ? (
          <p className="text-xs text-amber-700/90 dark:text-amber-400/90 leading-relaxed">{latest.error}</p>
        ) : stacks.length === 0 ? (
          <p className="text-xs text-muted-foreground">No containers</p>
        ) : stacks.map((stack) => (
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
                {stack.containers.map((c: DockerContainer) => (
                  <ContainerRow key={c.id} container={c} />
                ))}
              </div>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
