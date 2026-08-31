import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { Trash2, LogOut, ExternalLink, ArrowRight, Check, X } from 'lucide-react'
import { OSIcon } from '@/shared/components/OSIcon'
import { apiClient } from '@/shared/lib/api'
import { useHosts, useDeleteHost } from '@/widgets/hosts/useHosts'
import {
  usePendingChanges,
  useApprovePendingChange,
  useRejectPendingChange,
} from '@/widgets/hosts/usePendingChanges'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import {
  RaftClusterWidget,
  FormClusterWidget,
  useRaftStatus,
  useLeaveRaftCluster,
  useFactoryResetRaft,
} from '@/widgets/raft'

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

  // host → advertised HTTP URL: a cluster voter whose Raft advertise IP
  // matches the host's IPv4 carries the URL from the peer catalog. Lets the
  // admin tell same-named machines apart and jump to a node's own dashboard.
  const hostURL = (ipv4?: string): string | undefined => {
    if (!ipv4) return undefined
    const peer = raftStatus?.status?.peers?.find((p) => p.addr?.split(':')[0] === ipv4)
    if (!peer) return undefined
    return raftStatus?.peer_urls?.find((u) => u.node_id === peer.id)?.url
  }

  const deleteHost = useDeleteHost()
  const leaveCluster = useLeaveRaftCluster()
  const factoryReset = useFactoryResetRaft()

  // Frozen host-identity proposals (connector-detected rename / MAC change on
  // an existing host), parked for approval instead of overwriting the row.
  const { data: pendingChanges = [] } = usePendingChanges()
  const approveChange = useApprovePendingChange()
  const rejectChange = useRejectPendingChange()

  const onApproveChange = async (changeId: string, hostName: string) => {
    const { confirmed } = await confirmDialog({
      title: 'Apply this identity change?',
      description: `Apply the proposed identity change to "${hostName}"? The host row is updated cluster-wide.`,
      confirmText: 'Apply',
    })
    if (!confirmed) return
    approveChange.mutate(changeId, {
      onSuccess: () => toast.success(`Change applied to "${hostName}"`),
      onError: (e) => toast.error('Apply failed: ' + e.message),
    })
  }

  const onRejectChange = (changeId: string, hostName: string) => {
    rejectChange.mutate(changeId, {
      onSuccess: () => toast.success(`Change for "${hostName}" rejected`),
      onError: (e) => toast.error('Reject failed: ' + e.message),
    })
  }

  const onRemoveHost = async (id: number, name: string) => {
    const { confirmed } = await confirmDialog({
      title: 'Remove host?',
      description: `Remove host "${name}" and all of its metrics from the cluster? This cannot be undone.`,
      variant: 'destructive',
      confirmText: 'Remove',
    })
    if (!confirmed) return
    deleteHost.mutate(id, {
      onSuccess: () => toast.success(`Host "${name}" removed`),
      onError: (e) => toast.error('Remove failed: ' + e.message),
    })
  }

  const onLeaveCluster = async () => {
    const { confirmed } = await confirmDialog({
      title: 'Leave the Raft cluster?',
      description:
        'This node will be removed from the cluster and revert to standalone (Raft stopped, RAFT_* cleared from .env). Its local data is kept.',
      variant: 'destructive',
      confirmText: 'Leave',
    })
    if (!confirmed) return
    leaveCluster.mutate(undefined, {
      onSuccess: (r) => toast.success(r.next || 'Left the cluster'),
      onError: async (e) => {
        // A clean leave can fail when this node is wedged (e.g. a half-completed
        // join with no quorum, so no leader can process the membership removal).
        // Offer the force path: factory-reset wipes Raft state + RAFT_* from .env
        // and reverts to standalone without needing a leader.
        toast.error('Leave failed: ' + e.message)
        const { confirmed: force } = await confirmDialog({
          title: 'Force out with a factory reset?',
          description:
            'Leave failed — this node may be stuck (e.g. a failed cluster join with no quorum).\n\n' +
            'Force it out with a Factory reset? This wipes Raft state and removes RAFT_* from .env, ' +
            'reverting this node to standalone after a restart. Local data (users, hosts, metrics) is kept.',
          variant: 'destructive',
          confirmText: 'Factory reset',
        })
        if (force) {
          factoryReset.mutate(undefined, {
            onSuccess: () => toast.success('Raft factory-reset done. Restart the process to apply.'),
            onError: (err) => toast.error('Factory reset failed: ' + err.message),
          })
        }
      },
    })
  }
  const raftPanelVisible = raftEnabled || Boolean(raftStatus?.boot_error)
  // A standalone node (Raft off, no boot failure) gets the "form a cluster"
  // panel so an admin can bootstrap or join post-setup. Gated on a status
  // response having arrived (raftStatus defined) so the panel doesn't flash
  // before we know the node isn't already in a cluster.
  const formClusterVisible = Boolean(raftStatus) && !raftPanelVisible

  const pendingOnly = pendingChanges.filter((c) => c.status === 'pending')
  const rejectedChanges = pendingChanges.filter((c) => c.status === 'rejected')

  const accordionDefault = [
    ...(pendingOnly.length > 0 ? ['pending-changes'] : []),
    ...(hosts.length > 0 ? ['hosts'] : []),
    ...(raftPanelVisible ? ['raft'] : []),
    ...(formClusterVisible ? ['form-cluster'] : []),
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
          key={`${hosts.length}-${raftPanelVisible}-${formClusterVisible}-${pendingOnly.length > 0}`}
          multiple
          defaultValue={accordionDefault}
          className="w-full"
        >
          {pendingChanges.length > 0 && (
            <AccordionItem value="pending-changes" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>
                Pending identity changes
                {pendingOnly.length > 0 && (
                  <span className="ml-2 rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 font-mono text-[10px] font-normal text-amber-500 tabular-nums dark:text-amber-300">
                    {pendingOnly.length}
                  </span>
                )}
              </AccordionTrigger>
              <AccordionContent className="pb-4">
                <p className="mb-2 text-xs text-muted-foreground">
                  A connector (Proxmox / PBS) reports a different identity for these machines. The
                  rows are frozen until you apply or reject the change — nothing is overwritten
                  automatically.
                </p>
                <div className="divide-y divide-border/60 rounded-lg border border-border/60 dark:divide-white/10 dark:border-white/10">
                  {[...pendingOnly, ...rejectedChanges].map((ch) => (
                    <div key={ch.change_id} className="px-4 py-3">
                      <div className="flex items-center justify-between gap-2">
                        <div className="min-w-0">
                          <span className="flex items-center gap-2">
                            <span className="truncate font-medium">{ch.host_name || ch.host_mac}</span>
                            <span className="rounded border border-border/60 px-1 font-mono text-[10px] uppercase text-muted-foreground">
                              {ch.source}
                            </span>
                            {ch.status === 'rejected' && (
                              <span className="rounded border border-rose-500/40 bg-rose-500/10 px-1 font-mono text-[10px] uppercase text-rose-400">
                                rejected
                              </span>
                            )}
                          </span>
                          <div className="mt-1 space-y-0.5">
                            {ch.changes.map((fc) => (
                              <div
                                key={fc.field}
                                className="flex flex-wrap items-center gap-1.5 font-mono text-xs text-muted-foreground"
                              >
                                <span className="text-[10px] uppercase tracking-wider">{fc.field}</span>
                                <span className="truncate text-foreground/70 line-through decoration-muted-foreground/50">
                                  {fc.old || '—'}
                                </span>
                                <ArrowRight className="h-3 w-3 shrink-0 text-amber-500" />
                                <span className="truncate text-foreground">{fc.new}</span>
                              </div>
                            ))}
                          </div>
                        </div>
                        <div className="flex shrink-0 items-center gap-1.5">
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 gap-1 border-emerald-500/40 px-2 text-[11px] text-emerald-500 hover:bg-emerald-500/10 hover:text-emerald-400"
                            disabled={approveChange.isPending}
                            onClick={() => onApproveChange(ch.change_id, ch.host_name || ch.host_mac)}
                            title="Apply this change to the host row (cluster-wide)"
                          >
                            <Check className="h-3 w-3" /> Apply
                          </Button>
                          {ch.status !== 'rejected' && (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-7 gap-1 px-2 text-[11px] text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                              disabled={rejectChange.isPending}
                              onClick={() => onRejectChange(ch.change_id, ch.host_name || ch.host_mac)}
                              title="Reject — the connector stops proposing this value until it changes again"
                            >
                              <X className="h-3 w-3" /> Reject
                            </Button>
                          )}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </AccordionContent>
            </AccordionItem>
          )}

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
                    {hosts.map((host) => {
                      const isThisNode = localHostId !== undefined && host.id === localHostId
                      return (
                        <div key={host.id} className="px-4 py-3 transition-colors hover:bg-muted/20">
                          <div className="flex items-center justify-between gap-2">
                            <div className="flex min-w-0 items-center gap-2.5">
                              <OSIcon host={host} className="h-4 w-4 shrink-0 text-muted-foreground" />
                              <div className="min-w-0">
                                <span className="block truncate font-medium">{host.name}</span>
                                <span className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                                  {host.platform && <span>{host.platform}</span>}
                                  {host.origin_cluster && (
                                    <span
                                      className="rounded border border-violet-500/40 bg-violet-500/10 px-1 font-mono text-[10px] uppercase text-violet-500 dark:text-violet-300"
                                      title={`Uplinked from the "${host.origin_cluster}" cluster`}
                                    >
                                      {host.origin_cluster}
                                    </span>
                                  )}
                                  {host.ipv4 && <span className="font-mono">{host.ipv4}</span>}
                                  {(() => {
                                    const url = isThisNode ? window.location.origin : hostURL(host.ipv4)
                                    return url ? (
                                      <a
                                        href={url}
                                        target="_blank"
                                        rel="noreferrer"
                                        className="inline-flex max-w-[260px] items-center gap-0.5 truncate font-mono text-primary/80 hover:text-primary hover:underline"
                                        title={url}
                                      >
                                        <ExternalLink className="h-3 w-3 shrink-0" />
                                        <span className="truncate">{url.replace(/^https?:\/\//, '')}</span>
                                      </a>
                                    ) : null
                                  })()}
                                </span>
                              </div>
                            </div>
                            <div className="flex shrink-0 items-center gap-2">
                              {isThisNode ? (
                                <>
                                  <span className="rounded-full border border-primary/40 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-primary/80">
                                    this node
                                  </span>
                                  {raftEnabled && (
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      className="h-7 gap-1 border-destructive/40 px-2 text-[11px] text-destructive hover:bg-destructive/10 hover:text-destructive"
                                      disabled={leaveCluster.isPending}
                                      onClick={onLeaveCluster}
                                      title="Remove this node from the cluster and revert to standalone"
                                    >
                                      <LogOut className="h-3 w-3" /> Leave cluster
                                    </Button>
                                  )}
                                </>
                              ) : (
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  className="h-7 w-7 text-muted-foreground/60 hover:bg-destructive/10 hover:text-destructive"
                                  disabled={deleteHost.isPending}
                                  onClick={() => onRemoveHost(host.id, host.name)}
                                  title="Remove this host and its metrics from the cluster"
                                >
                                  <Trash2 className="h-3.5 w-3.5" />
                                </Button>
                              )}
                            </div>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                </ScrollArea>
              </AccordionContent>
            </AccordionItem>
          )}

          {formClusterVisible && (
            <AccordionItem value="form-cluster" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>
                Cluster sync
                <span className="ml-2 font-mono text-xs font-normal text-muted-foreground">
                  (standalone)
                </span>
              </AccordionTrigger>
              <AccordionContent className="pb-4">
                <FormClusterWidget />
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
