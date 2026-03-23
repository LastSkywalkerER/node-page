import { cn } from '@/lib/utils'
import { useConnectionStatus } from './useConnectionStatus'

interface ConnectionStatusWidgetProps {
  hostId?: number
}

export default function ConnectionStatusWidget({ hostId }: ConnectionStatusWidgetProps) {
  const { isConnected, latency, uptime, showUptime } = useConnectionStatus(hostId)

  const fmtUptime = (u: string | null) => u ?? '--'
  const fmtLatency = (ms: number | null) => ms == null || ms < 0 ? '--' : ms < 1 ? '<1ms' : `${Math.round(ms)}ms`

  return (
    <div className="flex items-center gap-2">
      <span className={cn('h-2 w-2 rounded-full', isConnected ? 'bg-green-500 animate-pulse' : 'bg-red-500')} />
      <span className="text-sm font-medium">{isConnected ? 'Connected' : 'Disconnected'}</span>
      {isConnected && latency !== null && (
        <span className="text-xs text-muted-foreground">{fmtLatency(latency)}</span>
      )}
      {isConnected && showUptime && uptime && (
        <span className="text-xs text-muted-foreground">up {fmtUptime(uptime)}</span>
      )}
    </div>
  )
}
