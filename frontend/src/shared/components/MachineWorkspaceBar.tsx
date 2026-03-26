import { useParams } from 'react-router-dom'
import { useHosts } from '@/widgets/hosts/useHosts'
import { getHostNavLabel } from '@/shared/lib/hostDisplay'
import ConnectionStatusWidget from '@/widgets/connection-status/ConnectionStatusWidget'
import { cn } from '@/lib/utils'

type Section = 'stats' | 'containers'

const SECTION_TITLE: Record<Section, string> = {
  stats: 'Live metrics',
  containers: 'Containers',
}

export function MachineWorkspaceBar({ section }: { section: Section }) {
  const { id } = useParams<{ id: string }>()
  const hostId = Number(id)
  const { data: hostsData } = useHosts()
  const hostRow = hostsData?.hosts?.find((h) => h.id === hostId)
  const hostName = hostRow ? getHostNavLabel(hostRow) : `Host #${id}`
  const title = SECTION_TITLE[section]

  return (
    <div
      className={cn(
        'mx-auto max-w-7xl px-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between',
        'py-4 border-b border-border/50 dark:border-white/10'
      )}
    >
      <div className="min-w-0 space-y-1">
        <p className="text-[11px] font-mono uppercase tracking-wider text-muted-foreground truncate">
          {hostName}
        </p>
        <h1 className="font-display text-lg font-semibold tracking-wide sm:text-xl">
          {title}
        </h1>
      </div>
      <div className="shrink-0 rounded-lg border border-border/60 bg-card/40 px-3 py-2 backdrop-blur-md dark:border-white/10 dark:bg-black/25">
        <ConnectionStatusWidget hostId={hostId} />
      </div>
    </div>
  )
}
