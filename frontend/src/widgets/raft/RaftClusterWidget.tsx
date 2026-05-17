import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Copy, Check, RefreshCw, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import {
  useRaftStatus,
  useIssueRaftJoinToken,
  useAddRaftPeer,
  useRemoveRaftPeer,
  useSaveRaftBridge,
  useResetRaftConfig,
  useWipeRaftState,
  BridgeSample,
} from './useRaft'

function fmtRTT(ns: number): string {
  if (!ns || ns < 0) return '—'
  const ms = ns / 1_000_000
  if (ms < 1) return `${(ms * 1000).toFixed(0)}µs`
  if (ms < 1000) return `${ms.toFixed(1)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function stateColor(state?: string): string {
  switch ((state || '').toLowerCase()) {
    case 'leader':
      return 'bg-emerald-500/15 text-emerald-300 border-emerald-500/40'
    case 'follower':
      return 'bg-sky-500/15 text-sky-300 border-sky-500/40'
    case 'candidate':
      return 'bg-amber-500/15 text-amber-300 border-amber-500/40'
    case 'shutdown':
      return 'bg-rose-500/15 text-rose-300 border-rose-500/40'
    default:
      return 'bg-muted text-muted-foreground border-border'
  }
}

/**
 * RaftClusterWidget shows the local cluster's role + peer list and, when
 * the cross-cluster bridge is configured, the per-URL RTT samples it has
 * collected from the peer cluster. From the admin "Raft" subtab.
 */
export function RaftClusterWidget() {
  const { data, isLoading, error, refetch, isFetching } = useRaftStatus(true)
  const issueToken = useIssueRaftJoinToken()
  const addPeer = useAddRaftPeer()
  const removePeer = useRemoveRaftPeer()

  const [issued, setIssued] = useState<{ token: string; expires_at: string } | null>(null)
  const [copied, setCopied] = useState(false)
  const [peerId, setPeerId] = useState('')
  const [peerAddr, setPeerAddr] = useState('')
  const [showManualAddVoter, setShowManualAddVoter] = useState(false)

  // Bridge config form (hot-update + persist in .env).
  const saveBridge = useSaveRaftBridge()
  const wipeState = useWipeRaftState()
  const [bridgeSecret, setBridgeSecret] = useState('')
  const [bridgeSeeds, setBridgeSeeds] = useState('')
  const [bridgeAdvertise, setBridgeAdvertise] = useState('')

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>
  }
  if (error) {
    return (
      <p className="text-sm text-destructive-foreground">
        Failed to load Raft status: {(error as Error).message}
      </p>
    )
  }
  if (!data?.status?.enabled) {
    return (
      <div className="space-y-3 text-sm text-muted-foreground">
        {data?.boot_error ? (
          <BootErrorBanner message={data.boot_error} />
        ) : (
          <p>
            Raft is disabled on this instance. Configure it from the setup wizard
            ("Start a new cluster") or set <code>RAFT_ENABLED=true</code> plus the
            related <code>RAFT_*</code> env vars and restart.
          </p>
        )}
      </div>
    )
  }

  const st = data.status

  const onIssue = async () => {
    try {
      const res = await issueToken.mutateAsync({ ttl_minutes: 60 })
      setIssued({ token: res.token, expires_at: res.expires_at })
      setCopied(false)
    } catch (e) {
      toast.error('Failed to issue join token: ' + (e as Error).message)
    }
  }

  const onCopy = async () => {
    if (!issued) return
    await navigator.clipboard.writeText(issued.token)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  const onAdd = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!peerId.trim() || !peerAddr.trim()) return
    try {
      await addPeer.mutateAsync({ id: peerId.trim(), addr: peerAddr.trim() })
      toast.success(`Voter ${peerId} added`)
      setPeerId('')
      setPeerAddr('')
    } catch (e) {
      toast.error('Add voter failed: ' + (e as Error).message)
    }
  }

  const onSaveBridge = async (e: React.FormEvent) => {
    e.preventDefault()
    const seeds = bridgeSeeds
      .split(',')
      .map((s) => s.trim().replace(/\/+$/, ''))
      .filter(Boolean)
    if (!bridgeSecret.trim()) {
      toast.error('Shared secret is required')
      return
    }
    try {
      await saveBridge.mutateAsync({
        shared_secret: bridgeSecret.trim(),
        remote_seeds: seeds,
        advertise_url: bridgeAdvertise.trim().replace(/\/+$/, '') || undefined,
      })
      toast.success('Bridge config applied and saved to .env')
    } catch (err) {
      toast.error('Save failed: ' + (err as Error).message)
    }
  }

  const onRemove = async (id: string) => {
    if (!window.confirm(`Remove Raft voter "${id}" from this cluster?`)) return
    try {
      await removePeer.mutateAsync(id)
      toast.success(`Voter ${id} removed`)
    } catch (e) {
      toast.error('Remove failed: ' + (e as Error).message)
    }
  }

  const onWipeState = async () => {
    const ok = window.confirm(
      'This will throw away the Raft consensus log + cluster membership on this node and re-bootstrap as a fresh single-voter cluster. ' +
        'Replicated data (users, hosts, metrics) is KEPT. Other voters will be orphaned and need to re-join. Continue?',
    )
    if (!ok) return
    try {
      await wipeState.mutateAsync()
      toast.success('Raft state wiped — node is now a fresh single-voter cluster')
    } catch (e) {
      toast.error('Wipe failed: ' + (e as Error).message)
    }
  }

  // Hashicorp returns the role with a leading capital letter ("Leader",
  // "Follower", "Candidate", "Shutdown"). Lower-case for comparison.
  const stuck =
    st.state &&
    st.state.toLowerCase() !== 'leader' &&
    st.state.toLowerCase() !== 'follower'

  return (
    <div className="space-y-5">
      <header className="flex items-center justify-between gap-3 flex-wrap">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm">{st.node_id || '—'}</span>
            <Badge variant="outline" className={stateColor(st.state)}>
              {st.state || 'unknown'}
            </Badge>
            <span className="text-xs text-muted-foreground">
              cluster <code>{st.cluster_id || '—'}</code>
            </span>
          </div>
          <p className="text-xs text-muted-foreground">
            term {st.term ?? 0} · applied {st.applied_index ?? 0} · commit {st.commit_index ?? 0}
            {' · leader '}
            <code>{st.leader_id || '—'}</code>
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void refetch()}
          disabled={isFetching}
          className="gap-1"
        >
          <RefreshCw className={`h-3.5 w-3.5 ${isFetching ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </header>

      {stuck && (
        <div className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 space-y-2 text-sm">
          <p className="font-medium text-amber-200">
            Cluster cannot elect a leader
          </p>
          <p className="text-xs text-amber-100/90">
            This usually means one of the voters in the list below has an
            unreachable advertise address (e.g. a Docker bridge IP that the
            other voters can't dial). The cluster needs majority of voters
            to be reachable to make progress.
          </p>
          <p className="text-xs text-amber-100/90">
            <strong>Wipe state</strong> throws away the consensus log + cluster
            membership and re-bootstraps THIS node as a fresh single-voter
            cluster. Replicated SQLite data (users, hosts, metrics) is kept.
            Use it when no other recovery option is available.
          </p>
          <Button
            size="sm"
            variant="outline"
            onClick={onWipeState}
            disabled={wipeState.isPending}
            className="border-amber-500/50 text-amber-100 hover:bg-amber-500/20"
          >
            {wipeState.isPending ? 'Wiping…' : 'Wipe state & re-bootstrap'}
          </Button>
        </div>
      )}

      <section className="space-y-2">
        <h4 className="text-sm font-display tracking-wide">Voters</h4>
        {st.peers && st.peers.length > 0 ? (
          <ul className="divide-y divide-border/50 rounded border border-border/50">
            {st.peers.map((p) => (
              <li key={p.id} className="flex items-center justify-between gap-3 px-3 py-2">
                <div className="font-mono text-xs">
                  <span className="font-medium">{p.id}</span>
                  <span className="text-muted-foreground"> @ {p.addr}</span>
                  <Badge variant="outline" className="ml-2 text-[10px]">
                    {p.suffrage}
                  </Badge>
                </div>
                {p.id !== st.node_id && st.state === 'Leader' ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onRemove(p.id)}
                    className="text-destructive hover:text-destructive"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">No peers known yet.</p>
        )}

        {st.state === 'Leader' && (
          <div className="space-y-2">
            <button
              type="button"
              onClick={() => setShowManualAddVoter((v) => !v)}
              className="text-xs text-muted-foreground underline hover:text-foreground"
            >
              {showManualAddVoter ? 'Hide advanced: manually add voter' : 'Advanced: manually add a voter (not needed for normal flow)'}
            </button>
            {showManualAddVoter && (
              <div className="space-y-2 rounded-md border border-border/50 bg-muted/10 p-3">
                <p className="text-xs text-muted-foreground">
                  Normally you'd issue a join token below and let the new node enrol itself
                  via the setup wizard. This form bypasses that — only use it if you know
                  the new node's id and Raft advertise address (host:port of its RAFT port,
                  not its HTTP port) and the node is already running.
                </p>
                <form onSubmit={onAdd} className="flex flex-col gap-2 sm:flex-row sm:items-end">
                  <div className="flex-1 space-y-1">
                    <label className="text-xs text-muted-foreground">Voter id</label>
                    <Input
                      value={peerId}
                      onChange={(e) => setPeerId(e.target.value)}
                      placeholder="vps-2"
                      className="h-9"
                    />
                  </div>
                  <div className="flex-1 space-y-1">
                    <label className="text-xs text-muted-foreground">Raft advertise addr (host:port)</label>
                    <Input
                      value={peerAddr}
                      onChange={(e) => setPeerAddr(e.target.value)}
                      placeholder="10.0.0.2:7000"
                      className="h-9"
                    />
                  </div>
                  <Button type="submit" size="sm" disabled={addPeer.isPending}>
                    Add voter
                  </Button>
                </form>
              </div>
            )}
          </div>
        )}
      </section>

      <section className="space-y-2">
        <h4 className="text-sm font-display tracking-wide">One-shot join token</h4>
        <p className="text-xs text-muted-foreground">
          Generate a token to bring up a fresh node. The new node pastes it into the setup
          wizard's "Join existing cluster" branch.
        </p>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            readOnly
            value={issued?.token ?? ''}
            placeholder="Click Generate to issue"
            className="h-9 flex-1 font-mono text-xs"
          />
          <div className="flex gap-2">
            <Button onClick={onIssue} size="sm" disabled={issueToken.isPending || st.state !== 'Leader'}>
              Generate
            </Button>
            <Button onClick={onCopy} size="sm" variant="outline" disabled={!issued}>
              {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            </Button>
          </div>
        </div>
        {issued ? (
          <p className="text-xs text-muted-foreground">
            Expires <time dateTime={issued.expires_at}>{new Date(issued.expires_at).toLocaleString()}</time>.
            Tokens are one-shot — they're stored hashed; this is the only time the plaintext appears.
          </p>
        ) : null}
        {st.state !== 'Leader' ? (
          <p className="text-xs text-amber-500">
            Only the cluster leader can issue tokens. This node is {st.state}.
          </p>
        ) : null}
      </section>

      <section className="space-y-2">
        <h4 className="text-sm font-display tracking-wide">Cross-cluster bridge</h4>
        <p className="text-xs text-muted-foreground">
          Configure the async link to the peer Raft cluster. Changes apply live
          (the running sender / picker / receiver are rebuilt) and persist to
          <code> .env</code> for the next restart.
        </p>
        <form onSubmit={onSaveBridge} className="space-y-2">
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">Shared HMAC secret</label>
            <Input
              type="password"
              value={bridgeSecret}
              onChange={(e) => setBridgeSecret(e.target.value)}
              placeholder="paste the secret shared between this cluster and the peer"
              className="h-9 font-mono text-xs"
            />
          </div>
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">Remote seed URLs (comma-separated)</label>
            <Input
              value={bridgeSeeds}
              onChange={(e) => setBridgeSeeds(e.target.value)}
              placeholder="https://vps1.example.com,https://vps2.example.com"
              className="h-9 font-mono text-xs"
            />
          </div>
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">This node's advertise URL (optional)</label>
            <Input
              value={bridgeAdvertise}
              onChange={(e) => setBridgeAdvertise(e.target.value)}
              placeholder={st.advertise_url || 'https://local-1.example.com'}
              className="h-9 font-mono text-xs"
            />
          </div>
          <div className="pt-1">
            <Button type="submit" size="sm" disabled={saveBridge.isPending}>
              {saveBridge.isPending ? 'Applying…' : 'Apply & save to .env'}
            </Button>
          </div>
        </form>
      </section>

      {data.bridge_samples && data.bridge_samples.length > 0 ? (
        <section className="space-y-2">
          <h4 className="text-sm font-display tracking-wide">Peer cluster URLs</h4>
          <p className="text-xs text-muted-foreground">
            Latency-probed nodes from the other Raft cluster. The lowest-RTT healthy URL is
            used by the bridge sender to ship replication batches.
          </p>
          <ul className="divide-y divide-border/50 rounded border border-border/50">
            {data.bridge_samples.map((s: BridgeSample) => (
              <li key={s.url} className="flex items-center justify-between gap-3 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-xs">{s.url}</p>
                  <p className="text-[11px] text-muted-foreground">
                    cluster <code>{s.cluster_id}</code> · node <code>{s.node_id}</code>
                    {s.last_err ? <span className="text-destructive"> · {s.last_err}</span> : null}
                  </p>
                </div>
                <Badge
                  variant="outline"
                  className={s.healthy ? 'bg-emerald-500/15 text-emerald-300 border-emerald-500/40' : 'bg-rose-500/15 text-rose-300 border-rose-500/40'}
                >
                  {fmtRTT(s.rtt)}
                </Badge>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  )
}

function BootErrorBanner({ message }: { message: string }) {
  const reset = useResetRaftConfig()
  const onReset = async () => {
    if (!window.confirm("This will remove RAFT_* lines from .env. Raft stays running until the next restart. Continue?")) return
    try {
      await reset.mutateAsync()
      toast.success("Raft config reset. Restart the process to apply.")
    } catch (e) {
      toast.error("Reset failed: " + (e as Error).message)
    }
  }
  return (
    <div className="rounded-md border border-rose-500/50 bg-rose-500/10 p-3 space-y-2">
      <p className="text-sm font-medium text-rose-200">
        Raft failed to activate at boot
      </p>
      <pre className="text-xs text-rose-200/90 whitespace-pre-wrap break-words">{message}</pre>
      <p className="text-xs text-rose-200/80">
        Common causes: the RAFT_BIND_ADDR port is already taken on the host
        (try <code>lsof -i :7000</code>), the data dir is unwritable, or the
        advertise address does not resolve. Fix the underlying issue and
        restart, or click below to wipe RAFT_* from .env.
      </p>
      <Button variant="outline" size="sm" onClick={onReset} disabled={reset.isPending}>
        {reset.isPending ? "Resetting…" : "Reset Raft config (wipe RAFT_* from .env)"}
      </Button>
    </div>
  )
}
