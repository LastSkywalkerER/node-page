import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { AlertTriangle, Check, Crown, Link2, Loader2, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
import { apiClient } from '@/shared/lib/api'
import { useStartCluster, useJoinCluster, useAdvertiseHints, type AdvertiseHints } from './useRaft'
import { useUserStore } from '@/shared/store/user'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** First advertise-URL candidate the server could actually reach. */
function bestCandidateURL(hints?: AdvertiseHints): string {
  return hints?.candidates.find((c) => c.reachable)?.url ?? ''
}

/**
 * TCP-probe results for the advertise-URL candidates, as clickable chips:
 * green = the server reached it (click to use), amber = it didn't.
 */
function CandidateChips({
  hints,
  selected,
  onPick,
}: {
  hints?: AdvertiseHints
  selected: string
  onPick: (url: string) => void
}) {
  if (!hints || hints.candidates.length === 0) return null
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap gap-1">
        {hints.candidates.map((c) => (
          <button
            key={c.url}
            type="button"
            disabled={!c.reachable}
            onClick={() => onPick(c.url)}
            title={c.reachable ? 'Reachable from this server — click to use' : `Unreachable: ${c.error ?? ''}`}
            className={cn(
              'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-[10px] transition-colors',
              c.reachable
                ? 'border-emerald-500/40 text-emerald-500 dark:text-emerald-300 hover:bg-emerald-500/10 cursor-pointer'
                : 'border-amber-500/40 text-amber-500 dark:text-amber-300 cursor-not-allowed opacity-80',
              c.url === selected && 'ring-1 ring-emerald-400/60 bg-emerald-500/10'
            )}
          >
            {c.reachable ? <Check className="h-2.5 w-2.5" /> : <X className="h-2.5 w-2.5" />}
            {c.url}
          </button>
        ))}
      </div>
      {!bestCandidateURL(hints) && (
        <p className="text-[11px] text-amber-500 dark:text-amber-300">
          None of the probed URLs is reachable from this server, so nothing was pre-filled — enter
          an address other nodes can open, or leave empty to derive it from the advertise address.
        </p>
      )}
    </div>
  )
}

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
  // null = untouched → show the server-derived default as the actual value.
  const [addrDraft, setAddrDraft] = useState<string | null>(null)
  const [urlDraft, setUrlDraft] = useState<string | null>(null)
  const [cidDraft, setCidDraft] = useState<string | null>(null)
  const start = useStartCluster()
  const { data: hints } = useAdvertiseHints()

  const advertiseAddr = addrDraft ?? hints?.raft_addr ?? ''
  const advertiseURL = urlDraft ?? bestCandidateURL(hints)
  const clusterID = cidDraft ?? 'local'

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
        Pre-filled with values detected on this machine — usually you just press the button. Change
        them only when other nodes must reach this one via a different address (VPN, NAT, proxy).
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="start-advertise-addr" className="text-xs">
            Advertise address
          </Label>
          <Input
            id="start-advertise-addr"
            placeholder="10.0.0.2:7000"
            value={advertiseAddr}
            onChange={(e) => setAddrDraft(e.target.value)}
          />
          <p className={fieldHint}>
            This machine's <span className="font-mono">IP:port</span> for cluster traffic — other
            nodes connect here. Pre-filled with the detected LAN IP; the Raft port{' '}
            <span className="font-mono">7000</span> starts listening once the cluster starts, so it
            can't be probed beforehand.
          </p>
        </div>
        <div className="space-y-1">
          <Label htmlFor="start-cluster-id" className="text-xs">
            Cluster ID
          </Label>
          <Input
            id="start-cluster-id"
            placeholder="local"
            value={clusterID}
            onChange={(e) => setCidDraft(e.target.value)}
          />
          <p className={fieldHint}>
            Just a name for this cluster — shows up in diagnostics and join tokens.{' '}
            <span className="font-mono">local</span> is fine.
          </p>
        </div>
      </div>
      <div className="space-y-1">
        <Label htmlFor="start-advertise-url" className="text-xs">
          Advertise HTTP URL <span className="text-muted-foreground">(optional)</span>
        </Label>
        <Input
          id="start-advertise-url"
          placeholder="left empty — derived from the advertise address"
          value={advertiseURL}
          onChange={(e) => setUrlDraft(e.target.value)}
        />
        <p className={fieldHint}>
          This node's web address as OTHER nodes can open it — used to forward writes to the leader
          and baked into the one-command agent install. Pre-filled with the first candidate the
          server could reach:
        </p>
        <CandidateChips hints={hints} selected={advertiseURL} onPick={(u) => setUrlDraft(u)} />
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
  const [addrDraft, setAddrDraft] = useState<string | null>(null)
  const [urlDraft, setUrlDraft] = useState<string | null>(null)
  const [ack, setAck] = useState(false)
  const [joining, setJoining] = useState(false)
  const { data: hints } = useAdvertiseHints()
  const advertiseAddr = addrDraft ?? hints?.raft_addr ?? ''
  const advertiseURL = urlDraft ?? bestCandidateURL(hints)
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
          Main node URL
        </Label>
        <Input
          id="join-peer-url"
          required
          placeholder="e.g. http://10.0.0.2:9090"
          value={peerURL}
          onChange={(e) => setPeerURL(e.target.value)}
        />
        <p className={fieldHint}>
          The web address of any node already in the cluster — the same URL you open its dashboard
          on. Must be reachable from this machine.
        </p>
      </div>

      <div className="space-y-1">
        <Label htmlFor="join-token" className="text-xs">
          Connect key
        </Label>
        <Input
          id="join-token"
          required
          placeholder="64-character one-shot token"
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <p className={fieldHint}>
          Generated on the main node: Admin → Nodes → Add a node → Generate. One-shot and expires in
          an hour — make a fresh one if rejected.
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="join-advertise-addr" className="text-xs">
            Advertise address
          </Label>
          <Input
            id="join-advertise-addr"
            placeholder="10.0.0.3:7000"
            value={advertiseAddr}
            onChange={(e) => setAddrDraft(e.target.value)}
          />
          <p className={fieldHint}>
            THIS machine's <span className="font-mono">IP:port</span> the cluster will use to reach
            it. Pre-filled with the detected LAN IP; the Raft port{' '}
            <span className="font-mono">7000</span> opens during the join.
          </p>
        </div>
        <div className="space-y-1">
          <Label htmlFor="join-advertise-url" className="text-xs">
            Advertise HTTP URL <span className="text-muted-foreground">(optional)</span>
          </Label>
          <Input
            id="join-advertise-url"
            placeholder="left empty — derived from the advertise address"
            value={advertiseURL}
            onChange={(e) => setUrlDraft(e.target.value)}
          />
          <p className={fieldHint}>
            THIS node's web address for peers (write forwarding). Pre-filled with the first
            candidate the server could reach:
          </p>
          <CandidateChips hints={hints} selected={advertiseURL} onPick={(u) => setUrlDraft(u)} />
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
