import { useEffect, useRef, useState } from 'react'
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
  useBridgeSecret,
  useResetRaftConfig,
  useWipeRaftState,
  useFactoryResetRaft,
  useProbeVoter,
  BridgeSample,
  ProbeVoterResult,
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
 * JoinCommandBlock renders the copy-paste one-liner that installs the Docker
 * agent on a fresh machine and joins it to THIS cluster (scripts/agent-join.sh:
 * docker checks → compose up → /setup/join-raft-cluster → snapshot wait).
 * The join URL prefers the leader's advertised URL; the browser origin is the
 * fallback — both must be reachable from the new machine.
 */
function JoinCommandBlock({ token, advertiseURL }: { token: string; advertiseURL?: string }) {
  const [copiedCmd, setCopiedCmd] = useState(false)
  const joinURL = advertiseURL || window.location.origin
  const command = [
    `NODE_STATS_JOIN_URL="${joinURL}" \\`,
    `NODE_STATS_JOIN_KEY="${token}" \\`,
    `bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/agent-join.sh)"`,
  ].join('\n')

  const onCopyCmd = async () => {
    await navigator.clipboard.writeText(command)
    setCopiedCmd(true)
    window.setTimeout(() => setCopiedCmd(false), 1500)
  }

  return (
    <div className="space-y-1.5">
      <p className="text-xs font-medium text-foreground">Run on the new machine (needs Docker):</p>
      <div className="relative">
        <pre className="overflow-x-auto rounded-md border border-border/60 bg-muted/20 p-3 pr-12 font-mono text-[11px] leading-relaxed dark:border-white/10 dark:bg-black/30">
          {command}
        </pre>
        <Button
          onClick={onCopyCmd}
          size="icon"
          variant="ghost"
          className="absolute right-1.5 top-1.5 h-7 w-7"
          aria-label="Copy install command"
        >
          {copiedCmd ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
        </Button>
      </div>
      <p className="text-[11px] text-muted-foreground">
        The script installs the stack, then the node attaches itself as a voter and pulls the
        cluster snapshot. If <span className="font-mono">{joinURL}</span> isn't reachable from that
        machine, replace it with an address that is.
      </p>
    </div>
  )
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
  const factoryReset = useFactoryResetRaft()
  const probeVoter = useProbeVoter()
  const [probeResults, setProbeResults] = useState<Record<string, ProbeVoterResult>>({})
  const [bridgeSecret, setBridgeSecret] = useState('')
  const [bridgeSeeds, setBridgeSeeds] = useState('')
  const [bridgeAdvertise, setBridgeAdvertise] = useState('')
  // null = untouched → mirror the active mode from status.
  const [modeDraft, setModeDraft] = useState<'push' | 'receive' | 'both' | null>(null)

  // Prefill the (write-only) secret field from the server once, so an admin
  // returning later can still copy it when enrolling another site.
  const { data: storedSecret } = useBridgeSecret()
  const secretPrefilled = useRef(false)
  useEffect(() => {
    if (secretPrefilled.current || !storedSecret) return
    secretPrefilled.current = true
    if (storedSecret.shared_secret) {
      setBridgeSecret((cur) => cur || storedSecret.shared_secret)
    }
  }, [storedSecret])
  const [copiedSecret, setCopiedSecret] = useState(false)
  const onCopySecret = async () => {
    if (!bridgeSecret.trim()) return
    await navigator.clipboard.writeText(bridgeSecret.trim())
    setCopiedSecret(true)
    window.setTimeout(() => setCopiedSecret(false), 1500)
  }

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

  const bridgeMode = modeDraft ?? data.bridge?.mode ?? 'push'

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
    if (bridgeMode === 'push' && seeds.length === 0) {
      toast.error('The hub URL is required in uplink mode')
      return
    }
    try {
      await saveBridge.mutateAsync({
        mode: bridgeMode,
        shared_secret: bridgeSecret.trim(),
        remote_seeds: seeds,
        advertise_url: bridgeAdvertise.trim().replace(/\/+$/, '') || undefined,
      })
      toast.success('Uplink config applied and saved to .env')
    } catch (err) {
      toast.error('Save failed: ' + (err as Error).message)
    }
  }

  const onGenerateSecret = () => {
    const buf = new Uint8Array(32)
    crypto.getRandomValues(buf)
    setBridgeSecret(Array.from(buf, (b) => b.toString(16).padStart(2, '0')).join(''))
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

  const onProbe = async (addr: string) => {
    try {
      const res = await probeVoter.mutateAsync(addr)
      setProbeResults((prev) => ({ ...prev, [addr]: res }))
    } catch (e) {
      setProbeResults((prev) => ({ ...prev, [addr]: { reachable: false, addr, error: (e as Error).message } }))
    }
  }

  const onFactoryReset = async () => {
    const ok = window.confirm(
      'This will wipe Raft state AND remove RAFT_* lines from .env on this node. ' +
        'After restart, this node will be Raft-disabled. SQLite data (users, hosts, metrics) is kept. ' +
        'Use this on EVERY node when the cluster is wedged and you want to start over via the wizard. Continue?',
    )
    if (!ok) return
    try {
      await factoryReset.mutateAsync()
      toast.success('Raft factory-reset done. Restart the process to apply.')
    } catch (e) {
      toast.error('Factory reset failed: ' + (e as Error).message)
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
          {data.raft_stats ? (
            <div className="rounded border border-amber-500/30 bg-black/20 p-2 font-mono text-[11px] text-amber-100/80 space-y-0.5">
              <p>
                term {data.raft_stats.term ?? '—'} ·
                last_log {data.raft_stats.last_log_index ?? '?'}/{data.raft_stats.last_log_term ?? '?'} ·
                commit {data.raft_stats.commit_index ?? '?'} ·
                applied {data.raft_stats.applied_index ?? '?'}
              </p>
              <p>
                num_peers {data.raft_stats.num_peers ?? '?'} ·
                last_contact {data.raft_stats.last_contact ?? '—'}
              </p>
              {data.raft_stats.latest_configuration ? (
                <p className="break-all">
                  config: <span className="text-amber-50">{data.raft_stats.latest_configuration}</span>
                </p>
              ) : null}
            </div>
          ) : null}
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
          <p className="text-xs text-amber-100/80 pt-2">
            <strong>Wipe state</strong> only resets THIS node — the other voters still
            have us in their config. For a true cluster-wide reset, click
            <strong> Factory reset</strong> on EVERY node, then re-run the setup wizard.
          </p>
          <Button
            size="sm"
            variant="outline"
            onClick={onFactoryReset}
            disabled={factoryReset.isPending}
            className="border-rose-500/50 text-rose-100 hover:bg-rose-500/20"
          >
            {factoryReset.isPending ? 'Resetting…' : 'Factory reset Raft on this node'}
          </Button>
        </div>
      )}

      <section className="space-y-2">
        <h4 className="text-sm font-display tracking-wide">Voters</h4>
        {st.peers && st.peers.length > 0 ? (
          <ul className="divide-y divide-border/50 rounded border border-border/50">
            {st.peers.map((p) => {
              const probe = probeResults[p.addr]
              return (
                <li key={p.id} className="flex flex-col gap-1 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="font-mono text-xs min-w-0 flex-1">
                    <span className="font-medium">{p.id}</span>
                    <span className="text-muted-foreground"> @ {p.addr}</span>
                    <Badge variant="outline" className="ml-2 text-[10px]">
                      {p.suffrage}
                    </Badge>
                    {probe ? (
                      probe.reachable ? (
                        <span className="ml-2 text-[11px] text-emerald-300">✓ reachable</span>
                      ) : (
                        <span className="ml-2 text-[11px] text-rose-300 break-all">
                          ✗ {probe.error || 'unreachable'}
                        </span>
                      )
                    ) : null}
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onProbe(p.addr)}
                      disabled={probeVoter.isPending}
                      title={`TCP-probe ${p.addr} from this server`}
                      className="text-[11px]"
                    >
                      {probeVoter.isPending && probeVoter.variables === p.addr
                        ? 'Probing…'
                        : 'Probe'}
                    </Button>
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
                  </div>
                </li>
              )
            })}
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
        <h4 className="text-sm font-display tracking-wide">Add a node</h4>
        <p className="text-xs text-muted-foreground">
          Generate a one-shot token, then run the command below on the new machine — it installs
          the Docker agent and joins this cluster in one go. (The token also works in the setup
          wizard's "Join existing cluster" branch.)
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
          <>
            <p className="text-xs text-muted-foreground">
              Expires <time dateTime={issued.expires_at}>{new Date(issued.expires_at).toLocaleString()}</time>.
              Tokens are one-shot — they're stored hashed; this is the only time the plaintext appears.
            </p>
            <JoinCommandBlock token={issued.token} advertiseURL={st.advertise_url} />
          </>
        ) : null}
        {st.state !== 'Leader' ? (
          <p className="text-xs text-amber-500">
            Only the cluster leader can issue tokens. This node is {st.state}.
          </p>
        ) : null}
      </section>

      <section className="space-y-2">
        <h4 className="text-sm font-display tracking-wide">Metrics uplink</h4>
        <p className="text-xs text-muted-foreground">
          Ship this cluster's hosts and metrics to a public hub cluster (one-way: the hub sees
          everything, this cluster keeps knowing only its own data), or turn this cluster into the
          hub that receives uplinks from several sites. Changes apply live and persist to{' '}
          <code>.env</code>.
        </p>

        <div className="flex gap-2">
          {(
            [
              ['push', 'Send to a hub', 'This site uplinks its hosts & metrics'],
              ['receive', 'Receive uplinks', 'This is the public hub'],
              ['both', 'Two-way pair', 'Legacy symmetric bridge'],
            ] as const
          ).map(([m, label, hint]) => (
            <button
              key={m}
              type="button"
              onClick={() => setModeDraft(m)}
              title={hint}
              className={`rounded-md border px-2.5 py-1.5 text-xs transition-colors ${
                bridgeMode === m
                  ? 'border-primary/60 bg-primary/10 text-primary'
                  : 'border-border/60 text-muted-foreground hover:text-foreground dark:border-white/10'
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        <form onSubmit={onSaveBridge} className="space-y-2">
          {bridgeMode === 'push' && (
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Hub URL</label>
              <Input
                value={bridgeSeeds}
                onChange={(e) => setBridgeSeeds(e.target.value)}
                placeholder="https://vps.example.com — the public cluster's web address"
                className="h-9 font-mono text-xs"
              />
              <p className="text-[11px] text-muted-foreground">
                Outbound-only: this site POSTs to the hub — no ports to open here.
              </p>
            </div>
          )}
          {bridgeMode === 'both' && (
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Remote seed URLs (comma-separated)</label>
              <Input
                value={bridgeSeeds}
                onChange={(e) => setBridgeSeeds(e.target.value)}
                placeholder="https://vps1.example.com,https://vps2.example.com"
                className="h-9 font-mono text-xs"
              />
            </div>
          )}
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">Shared HMAC secret</label>
            <div className="flex gap-2">
              <Input
                type="password"
                value={bridgeSecret}
                onChange={(e) => setBridgeSecret(e.target.value)}
                placeholder={
                  bridgeMode === 'receive'
                    ? 'generate one here, paste the same value on every site'
                    : 'paste the secret generated on the hub'
                }
                className="h-9 flex-1 font-mono text-xs"
              />
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={onCopySecret}
                disabled={!bridgeSecret.trim()}
                aria-label="Copy secret"
                title="Copy the secret to paste on the other sites"
              >
                {copiedSecret ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              </Button>
              {bridgeMode === 'receive' && (
                <Button type="button" size="sm" variant="outline" onClick={onGenerateSecret}>
                  Generate
                </Button>
              )}
            </div>
          </div>
          {bridgeMode !== 'push' && (
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">This node's advertise URL (optional)</label>
              <Input
                value={bridgeAdvertise}
                onChange={(e) => setBridgeAdvertise(e.target.value)}
                placeholder={st.advertise_url || 'https://local-1.example.com'}
                className="h-9 font-mono text-xs"
              />
            </div>
          )}
          <div className="pt-1">
            <Button type="submit" size="sm" disabled={saveBridge.isPending}>
              {saveBridge.isPending ? 'Applying…' : 'Apply & save to .env'}
            </Button>
          </div>
        </form>

        {bridgeMode === 'receive' && bridgeSecret && (
          <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-xs">
            On each site's admin panel (Nodes → Raft → Metrics uplink) pick{' '}
            <span className="font-medium">Send to a hub</span> and paste: Hub URL{' '}
            <span className="font-mono">{st.advertise_url || window.location.origin}</span>, secret{' '}
            <span className="font-mono">{bridgeSecret.slice(0, 8)}…</span> — grab the full
            value with the copy button next to the field above.
          </div>
        )}

        {data.bridge?.sender && (
          <p className="text-xs text-muted-foreground">
            Uplink status:{' '}
            {data.bridge.sender.last_ship_err ? (
              <span className="text-rose-300">failing — {data.bridge.sender.last_ship_err}</span>
            ) : data.bridge.sender.last_ship_at ? (
              <span className="text-emerald-400">
                last shipped {new Date(data.bridge.sender.last_ship_at).toLocaleTimeString()} ·{' '}
                {data.bridge.sender.shipped_total} entries total
              </span>
            ) : (
              <span>waiting for the first batch…</span>
            )}
            {data.bridge.sender.pending > 0 ? ` · ${data.bridge.sender.pending} pending` : ''}
          </p>
        )}

        {data.uplinks && data.uplinks.length > 0 && (
          <div className="space-y-1">
            <p className="text-xs font-medium">Uplinked sites</p>
            <ul className="divide-y divide-border/50 rounded border border-border/50">
              {data.uplinks.map((u) => (
                <li key={u.cluster_id} className="flex items-center justify-between gap-3 px-3 py-1.5 text-xs">
                  <span className="font-mono">{u.cluster_id}</span>
                  <span className="text-muted-foreground">
                    last entry {new Date(u.last_applied_at).toLocaleString()} · idx {u.last_origin_index}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
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
