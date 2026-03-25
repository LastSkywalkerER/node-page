import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useHosts } from '@/widgets/hosts/useHosts'
import { Copy, Check, Link2, Server, Trash2, Eye, EyeOff, Save, Unplug } from 'lucide-react'
import { toast } from 'sonner'

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
    <div className="rounded-xl border bg-card p-5 space-y-4">
      <h3 className="font-medium text-sm">Connected to main</h3>
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

  return (
    <div className="space-y-6">
      <h2 className="text-lg font-semibold">Nodes</h2>

      {/* Fixed-height block: link fills the same field after Generate (no layout jump). */}
      <div className="rounded-xl border bg-card p-4 space-y-3">
        <h3 className="text-sm font-medium">One-time join link</h3>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-stretch">
          <Input
            readOnly
            value={joinLinkGenerated ?? ''}
            placeholder="Click Generate to create a link"
            className="font-mono text-sm min-h-10 flex-1 min-w-0"
            aria-label="Generated join link"
          />
          <div className="flex gap-2 shrink-0">
            <Button
              type="button"
              size="sm"
              className="sm:self-stretch"
              onClick={() => createInviteMutation.mutate()}
              disabled={createInviteMutation.isPending}
            >
              <Link2 className="h-4 w-4 mr-2" />
              Generate
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="sm:self-stretch px-3"
              disabled={!joinLinkGenerated}
              onClick={handleCopyGeneratedLink}
              title="Copy link"
            >
              {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            </Button>
          </div>
        </div>
      </div>

      {clusterUi?.is_agent && <AgentConnectionSettings clusterUi={clusterUi} />}

      {clusterUi?.push_url && !clusterUi.is_agent && (
        <div className="flex items-center gap-2 text-xs rounded-lg border bg-card px-3 py-2">
          <span className="text-muted-foreground shrink-0">Push URL</span>
          <code className="flex-1 min-w-0 truncate font-mono">{clusterUi.push_url}</code>
          <Button type="button" size="sm" variant="outline" className="h-7 px-2 shrink-0" onClick={handleCopyPush}>
            {pushCopied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          </Button>
        </div>
      )}

      {hosts.length > 0 && (
        <div className="border rounded-xl divide-y overflow-hidden">
          {hosts.map(
            (host: { id: number; name: string; platform?: string; has_node_credential?: boolean }) => (
              <div key={host.id} className="hover:bg-muted/20 transition-colors px-4 py-3">
                <div className="flex flex-col gap-2 min-[480px]:flex-row min-[480px]:items-center min-[480px]:justify-between">
                  <div className="min-w-0">
                    <span className="font-medium truncate block">{host.name}</span>
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
      )}

      {connectVisible && (
        <div className="rounded-xl border bg-card p-5 space-y-4">
          <h3 className="font-medium text-sm">Connect this node</h3>
          <p className="text-xs text-muted-foreground">Paste the join link from the main node. One-time per link.</p>
          <div className="flex gap-2">
            <Input
              placeholder="https://main.example.com:8080/api/v1/nodes/join?token=..."
              value={joinLink}
              onChange={(e) => setJoinLink(e.target.value)}
              className="font-mono text-sm flex-1 min-w-0"
            />
            <Button
              size="sm"
              onClick={handleConnect}
              disabled={connectMutation.isPending || !joinLink.trim()}
              className="shrink-0"
            >
              <Server className="h-4 w-4 mr-2" />
              Connect
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
