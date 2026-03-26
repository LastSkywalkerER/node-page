import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useHosts } from '@/widgets/hosts/useHosts'
import { Copy, Check, Link2, Server, Trash2, Eye, EyeOff, Save, Unplug } from 'lucide-react'
import { toast } from 'sonner'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'

const nodeAccordionTrigger =
  'py-3 text-sm hover:no-underline font-display tracking-wide [&_[data-slot=accordion-trigger-icon]]:text-primary/80'

type ClusterUIStatus = {
  show_connect_block: boolean
  push_url: string
  is_agent: boolean
  has_remote_agents: boolean
  main_node_url?: string
  node_access_token?: string
}

type RegenerateData = {
  node_access_token: string
}

function AgentConnectionSettings({ clusterUi }: { clusterUi: ClusterUIStatus }) {
  const [mainUrl, setMainUrl] = useState(clusterUi.main_node_url ?? '')
  const [token, setToken] = useState(clusterUi.node_access_token ?? '')
  const [showToken, setShowToken] = useState(false)
  const queryClient = useQueryClient()

  useEffect(() => {
    setMainUrl(clusterUi.main_node_url ?? '')
    setToken(clusterUi.node_access_token ?? '')
  }, [clusterUi.main_node_url, clusterUi.node_access_token])

  const saveMutation = useMutation({
    mutationFn: async () => {
      await apiClient.put('/nodes/agent-cluster-config', {
        main_node_url: mainUrl.trim(),
        node_access_token: token.trim(),
      })
    },
    onSuccess: () => {
      toast.success('Connection settings saved')
      queryClient.invalidateQueries({ queryKey: ['nodes-cluster-ui-status'] })
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Save failed')
    },
  })

  const disconnectMutation = useMutation({
    mutationFn: async () => {
      await apiClient.delete('/nodes/agent-cluster-config')
    },
    onSuccess: () => {
      toast.success('Disconnected from main node')
      queryClient.invalidateQueries({ queryKey: ['nodes-cluster-ui-status'] })
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Disconnect failed')
    },
  })

  return (
    <div className="space-y-4 pt-0.5">
      <h3 className="font-display text-sm font-medium tracking-wide">Connected to main</h3>
      <p className="text-xs text-muted-foreground">
        This instance pushes metrics to the URL below. Update after regenerating a token on the main node.
      </p>
      <div className="space-y-2">
        <label className="text-xs text-muted-foreground">Main node URL</label>
        <Input
          value={mainUrl}
          onChange={(e) => setMainUrl(e.target.value)}
          className="font-mono text-sm"
          placeholder="https://main.example.com:8080"
        />
      </div>
      <div className="space-y-2">
        <label className="text-xs text-muted-foreground">NODE_ACCESS_TOKEN</label>
        <div className="flex gap-2">
          <Input
            type={showToken ? 'text' : 'password'}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            className="font-mono text-sm flex-1 min-w-0"
            placeholder="token"
            autoComplete="off"
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="shrink-0 px-2.5"
            onClick={() => setShowToken((v) => !v)}
            title={showToken ? 'Hide token' : 'Show token'}
          >
            {showToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </Button>
        </div>
      </div>
      <div className="flex gap-2">
        <Button
          type="button"
          size="sm"
          disabled={saveMutation.isPending || !mainUrl.trim() || !token.trim()}
          onClick={() => saveMutation.mutate()}
        >
          <Save className="h-4 w-4 mr-2" />
          Save connection
        </Button>
        <Button
          type="button"
          size="sm"
          variant="destructive"
          disabled={disconnectMutation.isPending}
          onClick={() => {
            if (!confirm('Disconnect from main node? Push will stop and connection credentials will be cleared.')) return
            disconnectMutation.mutate()
          }}
        >
          <Unplug className="h-4 w-4 mr-2" />
          Disconnect
        </Button>
      </div>
    </div>
  )
}

/** Remote host with push credential (has_node_credential from GET /hosts). */
function RemoteAgentActions({ hostId, hasNodeCredential }: { hostId: number; hasNodeCredential: boolean }) {
  const [token, setToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const queryClient = useQueryClient()

  const regenMutation = useMutation({
    mutationFn: async () => {
      const res = await apiClient.post<{ data: RegenerateData }>(`/nodes/hosts/${hostId}/regenerate-token`)
      return res.data.data
    },
    onSuccess: (data) => {
      setToken(data.node_access_token)
      toast.success('New token — copy below.')
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: async () => {
      await apiClient.delete(`/nodes/hosts/${hostId}`)
    },
    onSuccess: () => {
      toast.success('Node removed')
      queryClient.invalidateQueries({ queryKey: ['hosts'] })
      queryClient.invalidateQueries({ queryKey: ['nodes-cluster-ui-status'] })
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Delete failed')
    },
  })

  const copyToken = async () => {
    if (!token) return
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
      toast.success('Copied')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Copy failed')
    }
  }

  if (!hasNodeCredential) {
    return null
  }

  return (
    <div className="flex flex-col gap-2 items-stretch min-[480px]:items-end w-full min-[480px]:w-auto min-w-0">
      <div className="flex items-center gap-2 shrink-0">
        <Button
          type="button"
          size="sm"
          disabled={regenMutation.isPending}
          onClick={() => {
            if (!confirm('Regenerate? Old token stops working.')) return
            regenMutation.mutate()
          }}
        >
          Regenerate token
        </Button>
        <Button
          type="button"
          size="sm"
          variant="destructive"
          className="h-8 px-2.5"
          disabled={deleteMutation.isPending}
          onClick={() => {
            if (!confirm('Remove this node from main? All stored metrics for it will be deleted.')) return
            deleteMutation.mutate()
          }}
          title="Remove node"
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      </div>
      {token !== null && (
        <div className="w-full flex gap-1 items-start border-t border-border/60 pt-2">
          <pre className="flex-1 min-w-0 text-[11px] font-mono bg-muted/50 px-2 py-1.5 rounded break-all whitespace-pre-wrap">
            {token}
          </pre>
          <Button type="button" size="sm" variant="outline" className="h-8 px-2 shrink-0" onClick={copyToken}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          </Button>
        </div>
      )}
    </div>
  )
}

export function NodesTab() {
  const [joinLink, setJoinLink] = useState('')
  const [joinLinkGenerated, setJoinLinkGenerated] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [pushCopied, setPushCopied] = useState(false)
  const queryClient = useQueryClient()

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

  const { data: clusterUi } = useQuery({
    queryKey: ['nodes-cluster-ui-status'],
    queryFn: async () => {
      const res = await apiClient.get<{ data: ClusterUIStatus }>('/nodes/cluster-ui-status')
      return res.data.data
    },
  })

  const createInviteMutation = useMutation({
    mutationFn: async () => {
      const res = await apiClient.post<{ data: { link: string } }>('/nodes/invite')
      return res.data.data
    },
    onSuccess: (result) => {
      setJoinLinkGenerated(result.link)
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to create join link')
    },
  })

  const connectMutation = useMutation({
    mutationFn: async (link: string) => {
      const res = await apiClient.post<{ data: { host_id: number; main_url: string; message: string } }>(
        '/nodes/connect',
        { join_link: link }
      )
      return res.data.data
    },
    onSuccess: (result) => {
      toast.success(result.message)
      setJoinLink('')
      queryClient.invalidateQueries({ queryKey: ['hosts'] })
      queryClient.invalidateQueries({ queryKey: ['nodes-cluster-ui-status'] })
    },
    onError: (err: Error & { response?: { data?: { error?: string; detail?: string } } }) => {
      const msg = err.response?.data?.error || err.response?.data?.detail || err.message || 'Failed to connect'
      toast.error(msg)
    },
  })

  const handleCopyGeneratedLink = async () => {
    const link = joinLinkGenerated
    if (!link) return
    try {
      await navigator.clipboard.writeText(link)
      setCopied(true)
      toast.success('Copied')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Failed to copy')
    }
  }

  const handleCopyPush = async () => {
    const u = clusterUi?.push_url
    if (!u) return
    try {
      await navigator.clipboard.writeText(u)
      setPushCopied(true)
      toast.success('Copied')
      setTimeout(() => setPushCopied(false), 2000)
    } catch {
      toast.error('Failed to copy')
    }
  }

  const handleConnect = () => {
    const trimmed = joinLink.trim()
    if (!trimmed) {
      toast.error('Paste the join link first')
      return
    }
    connectMutation.mutate(trimmed)
  }

  const connectVisible = clusterUi !== undefined && clusterUi.show_connect_block

  const accordionDefault = [
    ...(clusterUi?.is_agent ? ['agent'] : ['ingest']),
    ...(hosts.length > 0 ? ['hosts'] : []),
    ...(connectVisible ? ['connect'] : []),
  ]

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Nodes</CardTitle>
        <CardDescription>
          Join links and push URL on the main node; agent connection if this instance reports upstream; host list and
          one-time connect when applicable.
        </CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        <Accordion
          key={`${Boolean(clusterUi?.is_agent)}-${hosts.length}-${connectVisible}`}
          multiple
          defaultValue={accordionDefault}
          className="w-full"
        >
          {clusterUi?.is_agent && (
            <AccordionItem value="agent" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>This instance → main (agent)</AccordionTrigger>
              <AccordionContent className="pb-4">
                <AgentConnectionSettings clusterUi={clusterUi} />
              </AccordionContent>
            </AccordionItem>
          )}

          {!clusterUi?.is_agent && (
            <AccordionItem value="ingest" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>Join links &amp; push URL</AccordionTrigger>
              <AccordionContent className="space-y-4 pb-4">
                <div className="space-y-2">
                  <p className="text-xs text-muted-foreground">One-time link for a new machine to join this server.</p>
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                    <Input
                      readOnly
                      value={joinLinkGenerated ?? ''}
                      placeholder="Click Generate to create a link"
                      className="h-9 min-h-0 flex-1 min-w-0 font-mono text-sm"
                      aria-label="Generated join link"
                    />
                    <div className="flex w-full gap-2 shrink-0 sm:w-auto">
                      <Button
                        type="button"
                        size="lg"
                        className="h-9 flex-1 sm:flex-initial sm:px-4"
                        onClick={() => createInviteMutation.mutate()}
                        disabled={createInviteMutation.isPending}
                      >
                        <Link2 className="h-4 w-4 mr-2" />
                        Generate
                      </Button>
                      <Button
                        type="button"
                        size="lg"
                        variant="outline"
                        className="h-9 w-10 shrink-0 border-primary/40 text-primary hover:bg-primary/10 px-0"
                        disabled={!joinLinkGenerated}
                        onClick={handleCopyGeneratedLink}
                        title="Copy link"
                        aria-label="Copy join link"
                      >
                        {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                      </Button>
                    </div>
                  </div>
                </div>

                {clusterUi?.push_url && (
                  <div className="flex flex-col gap-2 rounded-lg border border-border/60 bg-muted/20 px-3 py-3 sm:flex-row sm:items-center dark:border-white/10">
                    <span className="shrink-0 text-xs font-medium text-muted-foreground">Push URL</span>
                    <code className="min-w-0 flex-1 truncate font-mono text-xs">{clusterUi.push_url}</code>
                    <Button
                      type="button"
                      size="lg"
                      variant="outline"
                      className="h-9 w-full shrink-0 border-primary/35 sm:w-auto sm:px-3"
                      onClick={handleCopyPush}
                    >
                      {pushCopied ? (
                        <>
                          <Check className="h-4 w-4 sm:mr-1.5" />
                          <span className="sm:inline">Copied</span>
                        </>
                      ) : (
                        <>
                          <Copy className="h-4 w-4 sm:mr-1.5" />
                          <span className="sm:inline">Copy</span>
                        </>
                      )}
                    </Button>
                  </div>
                )}
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
                    {hosts.map(
                      (host: { id: number; name: string; platform?: string; has_node_credential?: boolean }) => (
                        <div key={host.id} className="px-4 py-3 transition-colors hover:bg-muted/20">
                          <div className="flex flex-col gap-2 min-[480px]:flex-row min-[480px]:items-center min-[480px]:justify-between">
                            <div className="min-w-0">
                              <span className="block truncate font-medium">{host.name}</span>
                              {host.platform && (
                                <span className="text-xs text-muted-foreground">{host.platform}</span>
                              )}
                            </div>
                            {localHostId !== undefined && host.id !== localHostId && (
                              <RemoteAgentActions
                                hostId={host.id}
                                hasNodeCredential={Boolean(host.has_node_credential)}
                              />
                            )}
                          </div>
                        </div>
                      )
                    )}
                  </div>
                </ScrollArea>
              </AccordionContent>
            </AccordionItem>
          )}

          {connectVisible && (
            <AccordionItem value="connect" className="border-border/50 dark:border-white/10">
              <AccordionTrigger className={nodeAccordionTrigger}>Connect this node to main</AccordionTrigger>
              <AccordionContent className="space-y-3 pb-4">
                <p className="text-xs text-muted-foreground">
                  Paste the join link from the main node. Each link works once.
                </p>
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <Input
                    placeholder="https://main.example.com:8080/api/v1/nodes/join?token=..."
                    value={joinLink}
                    onChange={(e) => setJoinLink(e.target.value)}
                    className="h-9 min-w-0 flex-1 font-mono text-sm"
                  />
                  <Button
                    size="lg"
                    className="h-9 w-full shrink-0 sm:w-auto"
                    onClick={handleConnect}
                    disabled={connectMutation.isPending || !joinLink.trim()}
                  >
                    <Server className="h-4 w-4 mr-2" />
                    Connect
                  </Button>
                </div>
              </AccordionContent>
            </AccordionItem>
          )}
        </Accordion>
      </CardContent>
    </Card>
  )
}
