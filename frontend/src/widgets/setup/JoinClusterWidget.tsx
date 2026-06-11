import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useCheckReachable, useRaftProgress } from './useSetup';
import { detectPublicUrl } from './detectPublicUrl';

export const JOIN_CLUSTER_STEP_META = {
  title: 'Join an existing cluster',
  description:
    'Paste the connect key an admin generated on the cluster. Everything else is auto-configured — the leader replicates users, hosts and history to this node.',
} as const;

export interface JoinClusterFormValues {
  peer_url: string;
  token: string;
  /** Optional override — only needed when peers must reach this node at a
   *  different public URL than the one auto-derived from its address (e.g.
   *  behind a reverse proxy). Left blank in the common case. */
  advertise_url: string;
}

interface JoinClusterWidgetProps {
  defaultPeerUrl?: string;
  isJoining: boolean;
  error: string | null;
  status: 'idle' | 'joining' | 'replicating';
  onBack: () => void;
  onSubmit: (values: JoinClusterFormValues) => void;
}

export function JoinClusterWidget({
  defaultPeerUrl = '',
  isJoining,
  error,
  status,
  onBack,
  onSubmit,
}: JoinClusterWidgetProps) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [peerUrl, setPeerUrl] = useState(defaultPeerUrl);
  const [token, setToken] = useState('');
  // Opened through a real domain (reverse proxy / public DNS) → that address
  // is what cluster peers should use to reach this node over HTTP.
  const [advertiseUrl, setAdvertiseUrl] = useState(() => detectPublicUrl());

  const busy = isJoining || status === 'replicating';

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!peerUrl.trim() || !token.trim()) return;
    onSubmit({
      peer_url: peerUrl.trim().replace(/\/+$/, ''),
      token: token.trim(),
      advertise_url: advertiseUrl.trim().replace(/\/+$/, ''),
    });
  };

  return (
    <form onSubmit={submit} className="space-y-5">
      <div className="space-y-1.5">
        <Label htmlFor="join-token">Connect key</Label>
        <Input
          id="join-token"
          autoFocus
          placeholder="paste the connect key generated on the first node"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          disabled={busy}
        />
        <p className="text-xs text-slate-400">
          One-shot token. Generate it on the first node's setup screen, or under{' '}
          <code>Admin → Nodes</code>.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="peer-url">Cluster node URL</Label>
        <Input
          id="peer-url"
          placeholder="http://192.168.0.104:8080"
          value={peerUrl}
          onChange={(e) => setPeerUrl(e.target.value)}
          disabled={busy}
        />
        <p className="text-xs text-slate-400">
          The URL of any node already in the cluster — the same address you open in a
          browser. We discover the leader, cluster id and our own advertise address
          automatically.
        </p>
      </div>

      <div className="space-y-3">
        <button
          type="button"
          onClick={() => setShowAdvanced((v) => !v)}
          className="text-xs text-slate-300 underline hover:text-slate-100"
          disabled={busy}
        >
          {showAdvanced ? 'Hide advanced' : 'Advanced (optional)'}
        </button>

        {!showAdvanced && advertiseUrl.trim() && (
          <p className="text-xs text-slate-400">
            Public URL: <code className="text-slate-300">{advertiseUrl.trim()}</code> —
            detected from this browser's address; the cluster will use it to reach this
            node. Change or clear it under Advanced.
          </p>
        )}

        {showAdvanced && (
          <div className="rounded-md border border-border/50 bg-muted/10 p-3 space-y-1.5">
            <Label htmlFor="join-public">Public URL override</Label>
            <Input
              id="join-public"
              placeholder="https://this-node.example.com"
              value={advertiseUrl}
              onChange={(e) => setAdvertiseUrl(e.target.value)}
              disabled={busy}
            />
            <p className="text-xs text-slate-400">
              The HTTP address other nodes use to reach this one. Prefilled from this
              browser's address when the page is opened through a domain (reverse proxy);
              clear it to fall back to the auto-detected LAN address.
            </p>
          </div>
        )}
      </div>

      {error ? (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive-foreground">
          {error}
        </div>
      ) : null}

      {status === 'replicating' ? <WaitingForSnapshot /> : null}

      <div className="flex justify-between gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onBack} disabled={busy}>
          Back
        </Button>
        <Button type="submit" disabled={busy || !peerUrl || !token}>
          {status === 'replicating' ? 'Waiting…' : isJoining ? 'Joining…' : 'Join cluster'}
        </Button>
      </div>
    </form>
  );
}

// WaitingForSnapshot is shown after the join HTTP exchange succeeds and the
// local Raft node has been added as a voter by the leader. It polls
// /setup/raft-progress every 2s to surface progress. After ~30 seconds with no
// committed entries it offers to TCP-probe this node's advertise address — if
// the joining node can't reach its own advertise address, neither can the
// leader, and snapshot replication will never start.
function WaitingForSnapshot() {
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
  const advertiseAddr = progress?.advertise_addr || '';

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
        The leader is shipping the current cluster snapshot (users, hosts, history) to
        this node. You'll be redirected to the login page as soon as it lands.
      </p>
      <p className="text-xs font-mono text-slate-300">
        local state: {state || '—'} · applied {appliedIdx} · commit {commitIdx} · leader:{' '}
        {leaderId || '—'} · elapsed {elapsedSec}s
      </p>

      {showDiag && (
        <div className="space-y-2 pt-2 border-t border-amber-500/30">
          <p className="text-xs text-amber-100/90">
            No snapshot in {elapsedSec}s. Most common cause: the leader can't reach this
            node on its advertise address{' '}
            {advertiseAddr ? <code>{advertiseAddr}</code> : null}. If this node is in
            Docker, make sure the nodes share a network (or the Raft port is published) and
            the container was recreated.
          </p>
          {advertiseAddr && (
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={onProbe}
                disabled={checkReachable.isPending}
                className="border-amber-500/50 text-amber-100 hover:bg-amber-500/20"
              >
                {checkReachable.isPending ? 'Probing…' : `Self-probe ${advertiseAddr}`}
              </Button>
              {reachableInfo &&
                (reachableInfo.reachable ? (
                  <span className="text-xs text-emerald-300">
                    ✓ Reachable from this server. The leader should reach it too.
                  </span>
                ) : (
                  <span className="text-xs text-rose-300 break-words">
                    ✗ Unreachable: {reachableInfo.error || 'unknown error'}. The leader
                    can't reach this address either.
                  </span>
                ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
