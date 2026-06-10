import { Link, useParams } from 'react-router-dom'
import { useHosts } from '@/widgets/hosts/useHosts'
import { getHostNavLabel } from '@/shared/lib/hostDisplay'
import ConnectionStatusWidget from '@/widgets/connection-status/ConnectionStatusWidget'
import { OSIcon } from '@/shared/components/OSIcon'
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
  // Guests expose their hypervisor in the crumb: "pve1 ▸ media-vm".
  const parentRow = hostRow?.parent_id
    ? hostsData?.hosts?.find((h) => h.id === hostRow.parent_id)
    : undefined
  const title = SECTION_TITLE[section]

  return (
    <div
      className={cn(
        'mx-auto max-w-7xl px-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between',
        'py-4 border-b border-border/50 dark:border-white/10'
      )}
    >
      <div className="min-w-0 space-y-1">
        <p className="flex items-center gap-1.5 text-[11px] font-mono uppercase tracking-wider text-muted-foreground truncate">
          {parentRow && (
            <>
              <OSIcon host={parentRow} className="h-3 w-3" />
              <Link to={`/machines/${parentRow.id}/stats`} className="hover:text-foreground">
                {getHostNavLabel(parentRow)}
              </Link>
              <span className="text-muted-foreground/50">▸</span>
            </>
          )}
          {hostRow && <OSIcon host={hostRow} className="h-3 w-3" />}
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
