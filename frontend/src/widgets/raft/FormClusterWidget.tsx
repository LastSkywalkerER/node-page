import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { AlertTriangle, Crown, Link2, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { apiClient } from '@/shared/lib/api'
import { useStartCluster, useJoinCluster } from './useRaft'
import { useUserStore } from '@/shared/store/user'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

function errMsg(e: unknown): string {
  const ax = e as {
    response?: { data?: { error?: string; detail?: string } }
    message?: string
  }
  return ax?.response?.data?.error || ax?.response?.data?.detail || ax?.message || 'Request failed'
}

const fieldHint = 'text-[11px] text-muted-foreground'

/**
 * Shown on a standalone node (Raft disabled) so an admin can form a cluster
 * post-setup — either bootstrap this node as the leader (non-destructive) or
 * join an existing cluster (destructive: replaces local data).
 */
export function FormClusterWidget() {
  const [mode, setMode] = useState<'none' | 'start' | 'join'>('none')

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        This node runs standalone. Form a cluster to sync metrics and applications across machines.
      </p>

      <div className="grid gap-3 sm:grid-cols-2">
        <button
          type="button"
          onClick={() => setMode('start')}
          className={`flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors ${
            mode === 'start'
              ? 'border-primary/60 bg-primary/5'
              : 'border-border/60 hover:bg-muted/20 dark:border-white/10'
          }`}
        >
          <span className="flex items-center gap-1.5 font-medium">
            <Crown className="h-4 w-4 text-primary" /> Make this the main node
          </span>
          <span className={fieldHint}>
            Bootstrap a new cluster with this node as leader. Keeps all local data.
          </span>
        </button>

        <button
          type="button"
          onClick={() => setMode('join')}
          className={`flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors ${
            mode === 'join'
              ? 'border-rose-500/60 bg-rose-500/5'
              : 'border-border/60 hover:bg-muted/20 dark:border-white/10'
          }`}
        >
          <span className="flex items-center gap-1.5 font-medium">
            <Link2 className="h-4 w-4 text-rose-400" /> Join another node
          </span>
          <span className={fieldHint}>
            Attach to an existing cluster using a connect key. Replaces local data.
          </span>
        </button>
      </div>

      {mode === 'start' && <StartClusterForm />}
      {mode === 'join' && <JoinClusterForm />}
    </div>
  )
}

function StartClusterForm() {
  const [advertiseAddr, setAdvertiseAddr] = useState('')
  const [advertiseURL, setAdvertiseURL] = useState('')
  const [clusterID, setClusterID] = useState('')
  const start = useStartCluster()

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    start.mutate(
      {
        cluster_id: clusterID.trim() || undefined,
        advertise_addr: advertiseAddr.trim() || undefined,
        advertise_url: advertiseURL.trim() || undefined,
      },
      {
        onSuccess: (r) => toast.success(r.data.message),
        onError: (err) => toast.error(errMsg(err)),
      },
    )
  }

  return (
    <form
      onSubmit={onSubmit}
      className="space-y-3 rounded-lg border border-primary/40 bg-primary/5 p-4"
    >
      <p className="text-sm font-medium">Start a new cluster</p>
      <p className={fieldHint}>
        Defaults are derived from this host. Set the advertise address to an IP:port other nodes can
        reach (only needed when nodes are on different machines).
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="start-advertise-addr" className="text-xs">
            Advertise address
          </Label>
          <Input
            id="start-advertise-addr"
            placeholder="auto (e.g. 10.0.0.2:7000)"
            value={advertiseAddr}
            onChange={(e) => setAdvertiseAddr(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="start-cluster-id" className="text-xs">
            Cluster ID
          </Label>
          <Input
            id="start-cluster-id"
            placeholder="local"
            value={clusterID}
            onChange={(e) => setClusterID(e.target.value)}
          />
        </div>
      </div>
      <div className="space-y-1">
        <Label htmlFor="start-advertise-url" className="text-xs">
          Advertise HTTP URL <span className="text-muted-foreground">(optional)</span>
        </Label>
        <Input
          id="start-advertise-url"
          placeholder="https://node1.example.com"
          value={advertiseURL}
          onChange={(e) => setAdvertiseURL(e.target.value)}
        />
        <p className={fieldHint}>Public base URL peers use to forward writes to this leader.</p>
      </div>
      <Button type="submit" size="sm" disabled={start.isPending}>
        {start.isPending ? 'Starting…' : 'Make this the main node'}
      </Button>
    </form>
  )
}

function JoinClusterForm() {
  const [peerURL, setPeerURL] = useState('')
  const [token, setToken] = useState('')
  const [advertiseAddr, setAdvertiseAddr] = useState('')
  const [advertiseURL, setAdvertiseURL] = useState('')
  const [ack, setAck] = useState(false)
  const [joining, setJoining] = useState(false)
  const aliveRef = useRef(true)
  const join = useJoinCluster()
  const navigate = useNavigate()
  const clearAuth = useUserStore((s) => s.clearAuth)

  // Cancel any in-flight readiness poll if the component unmounts.
  useEffect(() => () => void (aliveRef.current = false), [])

  // After the join is accepted the snapshot replicates ASYNCHRONOUSLY: the
  // local users + cluster JWT secrets only land once the FSM catches up, and
  // the backend swaps the secrets in shortly after. Redirecting on a blind
  // timer would strand the operator at a login they can't yet complete, so we
  // poll the public /setup/raft-progress until the node has caught up, then
  // hand off to /auth to sign in with the cluster's credentials.
  const waitForReplicationThenRedirect = async () => {
    const deadline = Date.now() + 2 * 60 * 1000
    while (aliveRef.current && Date.now() < deadline) {
      await sleep(2000)
      try {
        const { data } = await apiClient.get<{
          data: { enabled: boolean; applied_index: number; commit_index: number }
        }>('/setup/raft-progress')
        const p = data.data
        if (p.enabled && p.commit_index > 0 && p.applied_index >= p.commit_index) {
          // FSM has caught up; give the secret-swap poller a beat, then go.
          await sleep(3000)
          break
        }
      } catch {
        // Keep polling — transient errors are expected during cutover.
      }
    }
    if (!aliveRef.current) return
    clearAuth()
    navigate('/auth')
  }

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!ack) return
    join.mutate(
      {
        peer_url: peerURL.trim(),
        token: token.trim(),
        advertise_addr: advertiseAddr.trim() || undefined,
        advertise_url: advertiseURL.trim() || undefined,
        acknowledge_data_loss: true,
      },
      {
        onSuccess: (r) => {
          toast.success(r.data.message)
          setJoining(true)
          void waitForReplicationThenRedirect()
        },
        onError: (err) => toast.error(errMsg(err)),
      },
    )
  }

  if (joining) {
    return (
      <div className="space-y-3 rounded-lg border border-rose-500/40 bg-rose-500/5 p-4">
        <div className="flex items-center gap-2 text-sm">
          <Loader2 className="h-4 w-4 animate-spin text-rose-300" />
          <span className="font-medium text-rose-100">Joining cluster — replicating data…</span>
        </div>
        <p className="text-xs text-rose-100/80">
          Local data is being replaced with the cluster's state. You'll be redirected to sign in
          with the <span className="font-medium">cluster's</span> credentials once replication
          completes (up to ~2 min).
        </p>
      </div>
    )
  }

  return (
    <form
      onSubmit={onSubmit}
      className="space-y-3 rounded-lg border border-rose-500/40 bg-rose-500/5 p-4"
    >
      <div className="flex items-start gap-2 rounded-md border border-rose-500/40 bg-rose-500/10 p-3">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-rose-400" />
        <div className="space-y-1 text-xs text-rose-100/90">
          <p className="font-medium text-rose-200">This replaces all local data.</p>
          <p>
            Joining wipes this node's users, metrics history and settings, then replaces them with
            the cluster's state. You'll be signed out and must log in with the cluster's
            credentials.
          </p>
        </div>
      </div>

      <div className="space-y-1">
        <Label htmlFor="join-peer-url" className="text-xs">
          Cluster node URL
        </Label>
        <Input
          id="join-peer-url"
          required
          placeholder="https://main-node.example.com"
          value={peerURL}
          onChange={(e) => setPeerURL(e.target.value)}
        />
        <p className={fieldHint}>HTTP URL of any node already in the cluster.</p>
      </div>

      <div className="space-y-1">
        <Label htmlFor="join-token" className="text-xs">
          Connect key
        </Label>
        <Input
          id="join-token"
          required
          placeholder="one-shot join token from the leader"
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="join-advertise-addr" className="text-xs">
            Advertise address
          </Label>
          <Input
            id="join-advertise-addr"
            placeholder="auto (e.g. 10.0.0.3:7000)"
            value={advertiseAddr}
            onChange={(e) => setAdvertiseAddr(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="join-advertise-url" className="text-xs">
            Advertise HTTP URL <span className="text-muted-foreground">(optional)</span>
          </Label>
          <Input
            id="join-advertise-url"
            placeholder="https://node2.example.com"
            value={advertiseURL}
            onChange={(e) => setAdvertiseURL(e.target.value)}
          />
        </div>
      </div>

      <label className="flex items-center gap-2 text-xs text-rose-100/90">
        <input
          type="checkbox"
          checked={ack}
          onChange={(e) => setAck(e.target.checked)}
          className="h-3.5 w-3.5 accent-rose-500"
        />
        I understand this erases this node's local data.
      </label>

      <Button
        type="submit"
        size="sm"
        variant="outline"
        disabled={join.isPending || !ack}
        className="border-rose-500/50 text-rose-100 hover:bg-rose-500/20"
      >
        {join.isPending ? 'Joining…' : 'Join cluster'}
      </Button>
    </form>
  )
}
