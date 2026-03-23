import { useParams } from 'react-router-dom'
import { useMetricsStream } from '@/shared/hooks/useEventSource'
import { useLiveMetricsQuerySync } from '@/shared/hooks/useLiveMetricsQuerySync'
import { ErrorBoundary } from '@/shared/components/ErrorBoundary'
import { CPUWidget } from '@/widgets/cpu/CPUWidget'
import { MemoryWidget } from '@/widgets/memory/MemoryWidget'
import { DiskWidget } from '@/widgets/disk/DiskWidget'
import { NetworkWidget } from '@/widgets/network/NetworkWidget'
import SensorsWidget from '@/widgets/sensors/SensorsWidget'

export function MachineStatsPage() {
  const { id } = useParams<{ id: string }>()
  const hostId = Number(id)

  useMetricsStream(hostId)
  useLiveMetricsQuerySync(hostId)

  return (
    <div className="mx-auto max-w-7xl px-4 py-6">
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        <ErrorBoundary name="CPU"><CPUWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Memory"><MemoryWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Disk"><DiskWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Network"><NetworkWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Sensors"><SensorsWidget hostId={hostId} /></ErrorBoundary>
      </div>
    </div>
  )
}
