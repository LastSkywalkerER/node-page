import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { useHosts } from '@/widgets/hosts/useHosts'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { RaftClusterWidget, useRaftStatus } from '@/widgets/raft'

const nodeAccordionTrigger =
  'py-3 text-sm hover:no-underline font-display tracking-wide [&_[data-slot=accordion-trigger-icon]]:text-primary/80'

export function NodesTab() {
  const { data: hostsData } = useHosts()
  const hosts = hostsData?.hosts ?? []

  const { data: currentHost } = useQuery({
    queryKey: ['hosts', 'current'],
    queryFn: async () => {
      const res = await apiClient.get<{ host: { id: number } }>('/hosts/current')
      return res.data.host
    },
  })
  const localHostId = currentHost?.id

  // Raft is opt-in (RAFT_ENABLED=true); render the accordion item only when the
  // backend reports the layer as active, or when a boot-time activation failed
  // so the admin can read the error and recover.
  const { data: raftStatus } = useRaftStatus(true)
  const raftEnabled = Boolean(raftStatus?.status?.enabled)
  const raftPanelVisible = raftEnabled || Boolean(raftStatus?.boot_error)

  const accordionDefault = [
    ...(hosts.length > 0 ? ['hosts'] : []),
    ...(raftPanelVisible ? ['raft'] : []),
  ]

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Nodes</CardTitle>
        <CardDescription>
          Hosts known to this server and the Raft cluster. Add nodes with a connect key from the
          setup wizard or the Raft panel below.
        </CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        <Accordion
          key={`${hosts.length}-${raftPanelVisible}`}
          multiple
          defaultValue={accordionDefault}
          className="w-full"
        >
          {hosts.length > 0 && (
            <AccordionItem value="hosts" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>
                Registered hosts
                <span className="ml-2 font-mono text-xs font-normal text-muted-foreground tabular-nums">
                  ({hosts.length})
                </span>
              </AccordionTrigger>
              <AccordionContent className="pb-4">
                <ScrollArea className="h-[min(48vh,420px)] rounded-lg border border-border/60 dark:border-white/10">
                  <div className="divide-y divide-border/60 dark:divide-white/10">
                    {hosts.map((host: { id: number; name: string; platform?: string }) => (
                      <div key={host.id} className="px-4 py-3 transition-colors hover:bg-muted/20">
                        <div className="flex items-center justify-between gap-2">
                          <div className="min-w-0">
                            <span className="block truncate font-medium">{host.name}</span>
                            {host.platform && (
                              <span className="text-xs text-muted-foreground">{host.platform}</span>
                            )}
                          </div>
                          {localHostId !== undefined && host.id === localHostId && (
                            <span className="shrink-0 rounded-full border border-primary/40 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-primary/80">
                              this node
                            </span>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                </ScrollArea>
              </AccordionContent>
            </AccordionItem>
          )}

          {raftPanelVisible && (
            <AccordionItem value="raft" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>
                Raft cluster sync
                {raftStatus?.boot_error ? (
                  <span className="ml-2 font-mono text-xs font-normal text-rose-300">(boot failure)</span>
                ) : raftStatus?.status?.state ? (
                  <span className="ml-2 font-mono text-xs font-normal text-muted-foreground">
                    ({raftStatus.status.state})
                  </span>
                ) : null}
              </AccordionTrigger>
              <AccordionContent className="pb-4">
                <RaftClusterWidget />
              </AccordionContent>
            </AccordionItem>
          )}
        </Accordion>
      </CardContent>
    </Card>
  )
}
