import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Copy, Check, RefreshCw, Trash2, Download } from 'lucide-react'
import { toast } from 'sonner'
import { confirmDialog } from '@/shared/lib/confirmDialog'
import { copyToClipboard } from '@/shared/lib/clipboard'
import {
  useRaftStatus,
  useIssueRaftJoinToken,
  useAddRaftPeer,
  useRemoveRaftPeer,
  useSaveRaftBridge,
  useBridgeSettings,
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

const RAW_SCRIPTS = 'https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts'
const RELEASES_LATEST = 'https://github.com/LastSkywalkerER/node-page/releases/latest'

/** Every install path we ship, in the order shown in the platform selector. */
type InstallKind = 'linux-docker' | 'synology' | 'windows' | 'linux-native' | 'proxmox-lxc' | 'macos'
const INSTALL_KINDS: { id: InstallKind; label: string }[] = [
  { id: 'linux-docker', label: 'Linux · Docker' },
  { id: 'synology', label: 'Synology NAS' },
  { id: 'linux-native', label: 'Linux · native' },
  { id: 'proxmox-lxc', label: 'Proxmox · LXC' },
  { id: 'macos', label: 'macOS · native' },
  { id: 'windows', label: 'Windows · native' },
]

/** CopyButton — generic clipboard button with a transient check tick. */
function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const onCopy = async () => {
    await copyToClipboard(value)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }
  return (
    <Button onClick={onCopy} size="icon" variant="ghost" className="h-7 w-7" aria-label={label}>
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  )
}

/** CodeBlock — a copy-paste command box with a copy button in the corner. */
function CodeBlock({ code }: { code: string }) {
  return (
    <div className="relative">
      <pre className="overflow-x-auto rounded-md border border-border/60 bg-muted/20 p-3 pr-12 font-mono text-[11px] leading-relaxed dark:border-white/10 dark:bg-black/30">
        {code}
      </pre>
      <span className="absolute right-1.5 top-1.5">
        <CopyButton value={code} label="Copy command" />
      </span>
    </div>
  )
}

/** DownloadLink — prominent button-link to the latest release's native installers. */
function DownloadLink({ children }: { children: React.ReactNode }) {
  return (
    <a
      href={RELEASES_LATEST}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1.5 rounded-md border border-primary/50 bg-primary/10 px-2.5 py-1.5 text-xs font-medium text-primary transition-colors hover:bg-primary/20"
    >
      <Download className="h-3.5 w-3.5" />
      {children}
    </a>
  )
}

/** LabeledCopyField — a read-only value (cluster URL / token) with a copy button. */
function LabeledCopyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-[11px] text-muted-foreground">{label}</span>
      <Input readOnly value={value} className="h-8 flex-1 font-mono text-[11px]" />
      <CopyButton value={value} label={`Copy ${label}`} />
    </div>
  )
}

/**
 * WizardJoinSteps — for installers that don't auto-join (Windows, native,
 * Proxmox, macOS): after install you finish the join in the setup wizard's
 * "Join an existing cluster" branch, pasting the cluster URL + one-shot token.
 */
function WizardJoinSteps({ appUrl, joinURL, token }: { appUrl: string; joinURL: string; token: string }) {
  return (
    <div className="space-y-2 text-[11px] text-muted-foreground">
      <p>Then finish the join from the app's setup wizard:</p>
      <ol className="list-decimal space-y-1 pl-4">
        <li>
          Open <span className="font-mono">{appUrl}</span> on the new machine and continue setup.
        </li>
        <li>
          Pick <span className="font-medium text-foreground">"Join an existing cluster"</span>.
        </li>
        <li>Paste the cluster URL and the one-shot token below.</li>
      </ol>
      <div className="space-y-1.5 pt-1">
        <LabeledCopyField label="Cluster URL" value={joinURL} />
        <LabeledCopyField label="Join token" value={token} />
      </div>
    </div>
  )
}

/**
 * JoinCommandBlock renders a platform selector and the matching join recipe for
 * THIS cluster. Docker paths (Linux, Synology) get a single auto-join one-liner
 * (scripts/agent-join.sh: docker checks → compose up → /setup/join-raft-cluster
 * → snapshot wait). The other installers can't auto-join, so they show the
 * install command plus the wizard steps (cluster URL + token to paste). The
 * join URL prefers the leader's advertised URL; the browser origin is the
 * fallback — both must be reachable from the new machine.
 */
function JoinCommandBlock({ token, advertiseURL }: { token: string; advertiseURL?: string }) {
  const [kind, setKind] = useState<InstallKind>('linux-docker')
  const joinURL = advertiseURL || window.location.origin

  const body = () => {
    switch (kind) {
      case 'linux-docker':
        return (
          <>
            <p className="text-xs font-medium text-foreground">Run on the new machine (needs Docker):</p>
            <CodeBlock
              code={`NODE_STATS_JOIN_URL="${joinURL}" \\\nNODE_STATS_JOIN_KEY="${token}" \\\nbash -c "$(curl -fsSL ${RAW_SCRIPTS}/agent-join.sh)"`}
            />
            <p className="text-[11px] text-muted-foreground">
              Installs the Docker stack, attaches this node as a voter, and pulls the cluster
              snapshot. If <span className="font-mono">{joinURL}</span> isn't reachable from that
              machine, replace it with an address that is.
            </p>
          </>
        )
      case 'synology':
        return (
          <>
            <p className="text-xs font-medium text-foreground">
              Run over SSH on the NAS (needs Container Manager / Docker):
            </p>
            <CodeBlock
              code={`sudo NODE_STATS_DIR=/volume1/docker/node-stats \\\n  NODE_STATS_JOIN_URL="${joinURL}" \\\n  NODE_STATS_JOIN_KEY="${token}" \\\n  bash -c "$(curl -fsSL ${RAW_SCRIPTS}/agent-join.sh)"`}
            />
            <p className="text-[11px] text-muted-foreground">
              Enable SSH first (Control Panel → Terminal &amp; SNMP). x86 or 64-bit ARM models only.
              See the "Synology NAS" section in the README for the GUI / manual route.
            </p>
          </>
        )
      case 'windows':
        return (
          <>
            <p className="text-xs font-medium text-foreground">
              Windows (x64) — native installer (real host metrics, no Docker):
            </p>
            <DownloadLink>Download node-stats_*_windows_amd64_setup.exe</DownloadLink>
            <p className="text-[11px] text-muted-foreground">
              Run the installer from the latest release, then launch node-stats (Start menu) — it
              serves <span className="font-mono">http://localhost:8080</span>. Prefer Docker Desktop
              instead? <span className="font-mono">irm {RAW_SCRIPTS}/install.ps1 | iex</span>.
            </p>
            <WizardJoinSteps appUrl="http://localhost:8080" joinURL={joinURL} token={token} />
          </>
        )
      case 'linux-native':
        return (
          <>
            <p className="text-xs font-medium text-foreground">
              Install the native binary (no Docker, real host metrics):
            </p>
            <CodeBlock code={`curl -fsSL ${RAW_SCRIPTS}/install.sh | sudo bash -s -- native`} />
            <p className="text-[11px] text-muted-foreground">
              Prefer no script? Download the <span className="font-mono">.deb</span> or{' '}
              <span className="font-mono">_linux_&lt;arch&gt;.tar.gz</span> from the{' '}
              <a className="underline" href={RELEASES_LATEST} target="_blank" rel="noreferrer">
                latest release
              </a>
              .
            </p>
            <WizardJoinSteps appUrl="http://localhost:8080" joinURL={joinURL} token={token} />
          </>
        )
      case 'proxmox-lxc':
        return (
          <>
            <p className="text-xs font-medium text-foreground">Run on the Proxmox VE host (creates a Debian LXC):</p>
            <CodeBlock code={`bash -c "$(curl -fsSL ${RAW_SCRIPTS}/proxmox-lxc.sh)"`} />
            <WizardJoinSteps appUrl="http://<container-ip>:8080" joinURL={joinURL} token={token} />
          </>
        )
      case 'macos':
        return (
          <>
            <p className="text-xs font-medium text-foreground">macOS (Apple Silicon) — native app (real host metrics):</p>
            <DownloadLink>Download node-stats_*_darwin_arm64.dmg</DownloadLink>
            <p className="text-[11px] text-muted-foreground">
              Open the .dmg from the latest release, drag node-stats to Applications, launch it — it
              serves <span className="font-mono">http://localhost:8080</span>. CLI alternative: grab the{' '}
              <span className="font-mono">_darwin_arm64.tar.gz</span> and run{' '}
              <span className="font-mono">./node-stats</span>.
            </p>
            <WizardJoinSteps appUrl="http://localhost:8080" joinURL={joinURL} token={token} />
          </>
        )
    }
  }

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap gap-2">
        {INSTALL_KINDS.map((k) => (
          <button
            key={k.id}
            type="button"
            onClick={() => setKind(k.id)}
            className={`rounded-md border px-2.5 py-1.5 text-xs transition-colors ${
              kind === k.id
                ? 'border-primary/60 bg-primary/10 text-primary'
                : 'border-border/60 text-muted-foreground hover:text-foreground dark:border-white/10'
            }`}
          >
            {k.label}
          </button>
        ))}
      </div>
      <div className="space-y-1.5">{body()}</div>
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

  // Prefill the form from the saved configuration once: the secret is
  // otherwise unrecoverable after a reload, and an empty Hub URL that gets
  // re-applied would wipe the seeds and stall the uplink.
  const { data: savedBridge } = useBridgeSettings()
  const bridgePrefilled = useRef(false)
  useEffect(() => {
    if (bridgePrefilled.current || !savedBridge) return
    bridgePrefilled.current = true
    if (savedBridge.shared_secret) {
      setBridgeSecret((cur) => cur || savedBridge.shared_secret)
    }
    if (savedBridge.remote_seeds?.length) {
      setBridgeSeeds((cur) => cur || savedBridge.remote_seeds.join(','))
    }
    if (savedBridge.advertise_url) {
      setBridgeAdvertise((cur) => cur || savedBridge.advertise_url)
    }
  }, [savedBridge])
  const [copiedSecret, setCopiedSecret] = useState(false)
  const onCopySecret = async () => {
    if (!bridgeSecret.trim()) return
    await copyToClipboard(bridgeSecret.trim())
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
    await copyToClipboard(issued.token)
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
    const { confirmed } = await confirmDialog({
      title: 'Remove Raft voter?',
      description: `Remove Raft voter "${id}" from this cluster?`,
      variant: 'destructive',
      confirmText: 'Remove',
    })
    if (!confirmed) return
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
    const { confirmed } = await confirmDialog({
      title: 'Factory-reset Raft on this node?',
      description:
        'This will wipe Raft state AND remove RAFT_* lines from .env on this node. ' +
        'After restart, this node will be Raft-disabled. SQLite data (users, hosts, metrics) is kept. ' +
        'Use this on EVERY node when the cluster is wedged and you want to start over via the wizard.',
      variant: 'destructive',
      confirmText: 'Factory reset',
    })
    if (!confirmed) return
    try {
      await factoryReset.mutateAsync()
      toast.success('Raft factory-reset done. Restart the process to apply.')
    } catch (e) {
      toast.error('Factory reset failed: ' + (e as Error).message)
    }
  }

  const onWipeState = async () => {
    const { confirmed } = await confirmDialog({
      title: 'Recover this node as a single-node cluster?',
      description:
        'This drops the unreachable peer(s) and re-bootstraps this node as a healthy single-node cluster so it can accept writes again. ' +
        'Your data (users, hosts, metrics) is KEPT — only the Raft consensus log resets. Any other peers will need to re-join afterwards.',
      variant: 'destructive',
      confirmText: 'Recover (keep data)',
    })
    if (!confirmed) return
    try {
      await wipeState.mutateAsync()
      toast.success('Recovered — this node is now a healthy single-node cluster')
    } catch (e) {
      toast.error('Recovery failed: ' + (e as Error).message)
    }
  }

  // Hashicorp returns the role with a leading capital letter ("Leader",
  // "Follower", "Candidate", "Shutdown"). Lower-case for comparison.
  const role = (st.state || '').toLowerCase()
  // "stuck" = the cluster can't even elect a leader (Candidate/Shutdown).
  const stuck = role !== '' && role !== 'leader' && role !== 'follower'
  // "wedged" = we LOOK like a healthy follower but can't actually reach the
  // leader (one-way partition: its heartbeats reach us, our writes time out
  // forwarding to it). Without this the recovery banner never showed and the
  // survivor of a botched second node had no way out from the UI.
  const wedged = role === 'follower' && st.leader_reachable === false
  const needsRecovery = stuck || wedged
  const deadLeaderID = wedged ? st.leader_id : undefined

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

      {needsRecovery && (
        <div className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 space-y-2 text-sm">
          <p className="font-medium text-amber-200">
            {wedged ? 'Leader unreachable — writes are stuck' : 'Cluster cannot elect a leader'}
          </p>
          <p className="text-xs text-amber-100/90">
            {wedged ? (
              <>
                This node still receives heartbeats from{' '}
                <code>{deadLeaderID || 'the leader'}</code>, but it can&apos;t open a connection
                back to it (one-way partition — e.g. the leader advertises an address that went
                down). So this node stays a follower yet every write it forwards to the leader
                times out: you can&apos;t issue tokens, remove the peer, or delete its host. Restore
                connectivity to the leader to heal cleanly, or recover this node below.
              </>
            ) : (
              <>
                This usually means one of the voters in the list below has an unreachable advertise
                address (e.g. a Docker bridge IP that the other voters can&apos;t dial). The cluster
                needs majority of voters to be reachable to make progress.
              </>
            )}
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
            <strong>Recover this node</strong> drops the unreachable peer
            {deadLeaderID ? <> (<code>{deadLeaderID}</code>)</> : null} and
            re-bootstraps THIS node as a healthy single-node cluster, so it can
            elect itself leader and accept writes again. Your data —{' '}
            <strong>users, hosts and metrics are all kept</strong>; only the Raft
            consensus log resets. Any other peers will need to re-join afterwards.
          </p>
          <Button
            size="sm"
            variant="outline"
            onClick={onWipeState}
            disabled={wipeState.isPending}
            className="border-amber-500/50 text-amber-100 hover:bg-amber-500/20"
          >
            {wipeState.isPending ? 'Recovering…' : 'Recover this node (keep data)'}
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
          Generate a one-shot token, pick the new machine's platform, then run the command shown — it
          installs the agent and joins this cluster. Docker hosts join in one go; the other installers
          finish the join from their setup wizard's "Join an existing cluster" branch (same token).
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
    const { confirmed } = await confirmDialog({
      title: 'Reset Raft config?',
      description: 'This will remove RAFT_* lines from .env. Raft stays running until the next restart.',
      variant: 'destructive',
      confirmText: 'Reset',
    })
    if (!confirmed) return
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
