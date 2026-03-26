import { useParams } from 'react-router-dom'
import { useMetricsStream } from '@/shared/hooks/useEventSource'
import { useLiveMetricsQuerySync } from '@/shared/hooks/useLiveMetricsQuerySync'
import { ErrorBoundary } from '@/shared/components/ErrorBoundary'
import { CPUWidget } from '@/widgets/cpu/CPUWidget'
import { MemoryWidget } from '@/widgets/memory/MemoryWidget'
import { DiskWidget } from '@/widgets/disk/DiskWidget'
import { NetworkWidget } from '@/widgets/network/NetworkWidget'
import SensorsWidget from '@/widgets/sensors/SensorsWidget'
import { MachineWorkspaceBar } from '@/shared/components/MachineWorkspaceBar'

export function MachineStatsPage() {
  const { id } = useParams<{ id: string }>()
  const hostId = Number(id)

  useMetricsStream(hostId)
  useLiveMetricsQuerySync(hostId)

  return (
    <div className="mx-auto max-w-7xl">
      <MachineWorkspaceBar section="stats" />
      <div className="grid grid-cols-1 gap-5 px-4 pb-10 pt-2 md:grid-cols-2 xl:grid-cols-3">
        <ErrorBoundary name="CPU"><CPUWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Memory"><MemoryWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Disk"><DiskWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Network"><NetworkWidget hostId={hostId} /></ErrorBoundary>
        <ErrorBoundary name="Sensors">
          <div className="md:col-span-2 xl:col-span-3">
            <SensorsWidget hostId={hostId} />
          </div>
        </ErrorBoundary>
      </div>
    </div>
  )
}
