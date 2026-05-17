import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { MachineHintsResponse } from './schemas';

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
  isJoining: boolean;
  error: string | null;
  status: 'idle' | 'joining' | 'replicating';
  onBack: () => void;
  onSubmit: (values: JoinClusterFormValues) => void;
}

export function JoinClusterWidget({
  defaultPeerUrl = '',
  machineHints,
  isJoining,
  error,
  status,
  onBack,
  onSubmit,
}: JoinClusterWidgetProps) {
  const [showAdvanced, setShowAdvanced] = useState(false);
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
    if (!advertiseAddr && machineHints.suggested_ipv4) {
      setAdvertiseAddr(`${machineHints.suggested_ipv4}:7000`);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machineHints]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!peerUrl.trim() || !token.trim()) return;
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
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm">
          <p className="font-medium">Joined — waiting for snapshot replication…</p>
          <p className="text-xs text-slate-400 mt-1">
            The peer leader is shipping the current cluster snapshot (users, hosts, history) to this node.
            You'll be redirected to the login page as soon as it lands.
          </p>
        </div>
      ) : null}

      <div className="flex justify-between gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onBack} disabled={isJoining || status === 'replicating'}>
          Back
        </Button>
        <Button type="submit" disabled={isJoining || status === 'replicating' || !peerUrl || !token}>
          {status === 'replicating' ? 'Waiting…' : isJoining ? 'Joining…' : 'Join cluster'}
        </Button>
      </div>
    </form>
  );
}
