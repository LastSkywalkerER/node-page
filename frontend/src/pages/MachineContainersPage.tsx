import { useParams } from 'react-router-dom'
import { useMetricsStream } from '@/shared/hooks/useEventSource'
import { useLiveMetricsQuerySync } from '@/shared/hooks/useLiveMetricsQuerySync'
import { ErrorBoundary } from '@/shared/components/ErrorBoundary'
import { DockerWidget } from '@/widgets/docker/DockerWidget'

export function MachineContainersPage() {
  const { id } = useParams<{ id: string }>()
  const hostId = Number(id)

  useMetricsStream(hostId)
  useLiveMetricsQuerySync(hostId)

  return (
    <div className="mx-auto max-w-5xl px-4 py-6">
      <ErrorBoundary name="Docker">
        <DockerWidget hostId={hostId} />
      </ErrorBoundary>
    </div>
  )
}
