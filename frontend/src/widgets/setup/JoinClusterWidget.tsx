import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { MachineHintsResponse } from './schemas';
import { useCheckReachable, useRaftProgress } from './useSetup';

export const JOIN_CLUSTER_STEP_META = {
  title: 'Join an existing cluster',
  description:
    'Provide a peer node URL and the one-shot join token an admin issued on the cluster. The leader will replicate users, hosts and history to this node — no second wizard step required.',
} as const;

export interface JoinClusterFormValues {
  peer_url: string;
  token: string;
  node_id: string;
  bind_addr: string;
  advertise_addr: string;
  advertise_url: string;
  bridge_shared_secret: string;
  bridge_remote_seeds: string;
}

interface JoinClusterWidgetProps {
  defaultPeerUrl?: string;
  machineHints?: MachineHintsResponse | null;
  /** Server reports it's running inside a Docker container; auto-detected
   *  IPs in that case are typically the container's internal bridge address
   *  (172.x.x.x) and not reachable by peers outside Docker. */
  runningInDocker?: boolean;
  isJoining: boolean;
  error: string | null;
  status: 'idle' | 'joining' | 'replicating';
  onBack: () => void;
  onSubmit: (values: JoinClusterFormValues) => void;
}

// dockerBridgeIP guesses whether an IPv4 looks like a Docker bridge
// (default network 172.17.0.0/16 — 172.31.x.x range). Used to decide
// whether the auto-detected IP is safe to use for the Raft advertise
// address. Conservative: only flags ranges Docker actually allocates
// for its default bridges.
function looksLikeDockerBridge(ip: string): boolean {
  const parts = ip.trim().split('.');
  if (parts.length !== 4) return false;
  const a = Number(parts[0]);
  const b = Number(parts[1]);
  if (a !== 172) return false;
  return b >= 16 && b <= 31;
}

export function JoinClusterWidget({
  defaultPeerUrl = '',
  machineHints,
  runningInDocker,
  isJoining,
  error,
  status,
  onBack,
  onSubmit,
}: JoinClusterWidgetProps) {
  const ipLooksDocker = Boolean(
    machineHints?.suggested_ipv4 && looksLikeDockerBridge(machineHints.suggested_ipv4),
  );
  const dockerWarn = Boolean(runningInDocker) || ipLooksDocker;

  const [showAdvanced, setShowAdvanced] = useState(dockerWarn);
  const [peerUrl, setPeerUrl] = useState(defaultPeerUrl);
  const [token, setToken] = useState('');
  const [nodeId, setNodeId] = useState('');
  const [bindAddr, setBindAddr] = useState(':7000');
  const [advertiseAddr, setAdvertiseAddr] = useState('');
  const [advertiseUrl, setAdvertiseUrl] = useState('');
  const [bridgeSecret, setBridgeSecret] = useState('');
  const [bridgeSeeds, setBridgeSeeds] = useState('');

  useEffect(() => {
    if (!machineHints) return;
    if (!nodeId && machineHints.suggested_hostname) {
      setNodeId(machineHints.suggested_hostname.toLowerCase().replace(/\s+/g, '-'));
    }
    // Only auto-fill advertise_addr when the detected IP is plausibly
    // reachable from peers. Inside Docker (or when the IP is on the
    // container-internal bridge) we deliberately leave it blank so the
    // operator must enter a reachable host IP + a mapped port.
    if (!advertiseAddr && machineHints.suggested_ipv4 && !dockerWarn) {
      setAdvertiseAddr(`${machineHints.suggested_ipv4}:7000`);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machineHints, dockerWarn]);

  // When the joining node is inside Docker we require the operator to
  // explicitly enter an externally-reachable advertise_addr — otherwise
  // the leader (typically on the host network or another container)
  // can't dial the new voter and the cluster ends up wedged in
  // Candidate state forever. The leader has no way to recover without
  // wiping the Raft state.
  const advertiseAddrLooksDocker = looksLikeDockerBridge(advertiseAddr.split(':')[0] || '');
  const dockerBlocked = dockerWarn && (!advertiseAddr.trim() || advertiseAddrLooksDocker);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!peerUrl.trim() || !token.trim()) return;
    if (dockerBlocked) return;
    onSubmit({
      peer_url: peerUrl.trim().replace(/\/+$/, ''),
      token: token.trim(),
      node_id: nodeId.trim(),
      bind_addr: bindAddr.trim(),
      advertise_addr: advertiseAddr.trim(),
      advertise_url: advertiseUrl.trim().replace(/\/+$/, ''),
      bridge_shared_secret: bridgeSecret.trim(),
      bridge_remote_seeds: bridgeSeeds.trim(),
    });
  };

  return (
    <form onSubmit={submit} className="space-y-5">
      {dockerWarn && (
        <div className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm space-y-1">
          <p className="font-medium text-amber-200">
            This node is running inside Docker
          </p>
          <p className="text-xs text-amber-100/90">
            The auto-detected IP{' '}
            {machineHints?.suggested_ipv4 ? (
              <code>{machineHints.suggested_ipv4}</code>
            ) : null}{' '}
            is the container's internal address and is <strong>not</strong> reachable
            from the cluster leader. Open the <em>advanced node settings</em> below and
            enter a host:port the leader can dial — typically the host's external IP
            with a port you've mapped in your <code>docker-compose.yml</code> (e.g.{' '}
            <code>192.168.0.104:7002</code>, with <code>7002:7000</code> in ports).
          </p>
        </div>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="peer-url">Peer URL</Label>
        <Input
          id="peer-url"
          autoFocus
          placeholder="http://192.168.0.104:8080"
          value={peerUrl}
          onChange={(e) => setPeerUrl(e.target.value)}
          disabled={isJoining || status === 'replicating'}
        />
        <p className="text-xs text-slate-400">
          The <strong>HTTP API</strong> URL of any node in the cluster — the same URL you
          open in a browser (typically port 8080). <strong>Not</strong> the Raft transport
          port (7000). The peer will forward us to its leader if needed.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="join-token">One-shot join token</Label>
        <Input
          id="join-token"
          placeholder="paste the token an admin issued on the cluster"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          disabled={isJoining || status === 'replicating'}
        />
        <p className="text-xs text-slate-400">
          Generated via the admin panel <code>/admin/nodes → Raft → Issue join token</code>.
        </p>
      </div>

      <div className="space-y-3">
        <button
          type="button"
          onClick={() => setShowAdvanced((v) => !v)}
          className="text-xs text-slate-300 underline hover:text-slate-100"
          disabled={isJoining || status === 'replicating'}
        >
          {showAdvanced ? 'Hide advanced node settings' : 'Show advanced node settings (auto-filled)'}
        </button>

        {showAdvanced && (
          <div className="rounded-md border border-border/50 bg-muted/10 p-3 space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="join-node-id">Raft node ID</Label>
              <Input
                id="join-node-id"
                placeholder="local-2 (auto-filled from hostname)"
                value={nodeId}
                onChange={(e) => setNodeId(e.target.value)}
                disabled={isJoining || status === 'replicating'}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="join-bind">Raft bind addr</Label>
              <Input
                id="join-bind"
                placeholder=":7000"
                value={bindAddr}
                onChange={(e) => setBindAddr(e.target.value)}
                disabled={isJoining || status === 'replicating'}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="join-advertise">Advertise addr</Label>
              <Input
                id="join-advertise"
                placeholder="10.0.0.2:7000 (peers dial this)"
                value={advertiseAddr}
                onChange={(e) => setAdvertiseAddr(e.target.value)}
                disabled={isJoining || status === 'replicating'}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="join-public">Advertise public URL (optional)</Label>
              <Input
                id="join-public"
                placeholder="https://local-2.example.com"
                value={advertiseUrl}
                onChange={(e) => setAdvertiseUrl(e.target.value)}
                disabled={isJoining || status === 'replicating'}
              />
              <p className="text-xs text-slate-400">
                URL the cross-cluster bridge publishes for this node. Skip if not bridging.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="join-bridge-secret">Bridge shared secret (optional)</Label>
              <Input
                id="join-bridge-secret"
                type="password"
                placeholder="shared HMAC secret if joining a bridged cluster"
                value={bridgeSecret}
                onChange={(e) => setBridgeSecret(e.target.value)}
                disabled={isJoining || status === 'replicating'}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="join-bridge-seeds">Bridge remote seeds (optional)</Label>
              <Input
                id="join-bridge-seeds"
                placeholder="https://public-1.example.com,https://public-2.example.com"
                value={bridgeSeeds}
                onChange={(e) => setBridgeSeeds(e.target.value)}
                disabled={isJoining || status === 'replicating'}
              />
            </div>
          </div>
        )}
      </div>

      {error ? (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive-foreground">
          {error}
        </div>
      ) : null}

      {status === 'replicating' ? (
        <WaitingForSnapshot advertiseAddr={advertiseAddr.trim() || `${bindAddr}`} />
      ) : null}

      <div className="flex justify-between gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onBack} disabled={isJoining || status === 'replicating'}>
          Back
        </Button>
        <Button
          type="submit"
          disabled={
            isJoining || status === 'replicating' || !peerUrl || !token || dockerBlocked
          }
          title={
            dockerBlocked
              ? 'Enter a non-Docker advertise host:port in advanced settings — the leader cannot reach the container-internal address.'
              : undefined
          }
        >
          {status === 'replicating' ? 'Waiting…' : isJoining ? 'Joining…' : 'Join cluster'}
        </Button>
      </div>
    </form>
  );
}

// WaitingForSnapshot is shown after the join HTTP exchange succeeds and
// the local Raft node has been added as a voter by the leader. It polls
// /raft/status every 2s to surface progress (applied / commit indices,
// leader id). After ~30 seconds without any committed entries it offers
// to TCP-probe the advertise address from this same server — if the
// joining node can't reach its own advertise address, neither can the
// cluster leader, and snapshot replication will never start.
function WaitingForSnapshot({ advertiseAddr }: { advertiseAddr: string }) {
  const { data: progress } = useRaftProgress(true);
  const [elapsedSec, setElapsedSec] = useState(0);
  const [reachableInfo, setReachableInfo] = useState<null | {
    reachable: boolean;
    error?: string;
  }>(null);
  const checkReachable = useCheckReachable();

  useEffect(() => {
    const startedAt = Date.now();
    const id = window.setInterval(() => {
      setElapsedSec(Math.floor((Date.now() - startedAt) / 1000));
    }, 1000);
    return () => window.clearInterval(id);
  }, []);

  const appliedIdx = progress?.applied_index ?? 0;
  const commitIdx = progress?.commit_index ?? 0;
  const leaderId = progress?.leader_id || '';
  const state = (progress?.state || '').toLowerCase();

  const stuck = elapsedSec >= 30 && appliedIdx === 0;
  const showDiag = stuck || reachableInfo !== null;

  const onProbe = async () => {
    if (!advertiseAddr) return;
    setReachableInfo(null);
    try {
      const res = await checkReachable.mutateAsync(advertiseAddr);
      setReachableInfo({ reachable: res.reachable, error: res.error });
    } catch (e) {
      setReachableInfo({ reachable: false, error: (e as Error).message });
    }
  };

  return (
    <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-3 text-sm space-y-2">
      <p className="font-medium">Joined — waiting for snapshot replication…</p>
      <p className="text-xs text-slate-400">
        The peer leader is shipping the current cluster snapshot (users, hosts, history)
        to this node. You'll be redirected to the login page as soon as it lands.
      </p>
      <p className="text-xs font-mono text-slate-300">
        local state: {state || '—'} · applied {appliedIdx} · commit {commitIdx} · leader:{' '}
        {leaderId || '—'} · elapsed {elapsedSec}s
      </p>

      {showDiag && (
        <div className="space-y-2 pt-2 border-t border-amber-500/30">
          <p className="text-xs text-amber-100/90">
            No snapshot in {elapsedSec}s. Most common cause: the leader can't reach this
            node on its advertise address <code>{advertiseAddr}</code>. If you're in
            Docker, make sure the Raft port is mapped in <code>docker-compose.yml</code>
            (e.g. <code>ports: [&quot;7002:7002&quot;]</code>) and the container was
            recreated to pick it up.
          </p>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onProbe}
              disabled={checkReachable.isPending || !advertiseAddr}
              className="border-amber-500/50 text-amber-100 hover:bg-amber-500/20"
            >
              {checkReachable.isPending
                ? 'Probing…'
                : `Self-probe ${advertiseAddr}`}
            </Button>
            {reachableInfo &&
              (reachableInfo.reachable ? (
                <span className="text-xs text-emerald-300">
                  ✓ Reachable from this server. The leader should be able to dial it too.
                  If snapshot still doesn't arrive, check the leader's logs.
                </span>
              ) : (
                <span className="text-xs text-rose-300 break-words">
                  ✗ Unreachable from this server: {reachableInfo.error || 'unknown error'}.
                  The leader can't reach this address either — fix port mapping and re-do
                  the wizard.
                </span>
              ))}
          </div>
        </div>
      )}
    </div>
  );
}
