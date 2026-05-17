import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

export const JOIN_CLUSTER_STEP_META = {
  title: 'Join an existing cluster',
  description:
    'Provide a peer node URL and the one-shot join token an admin issued on the cluster. The leader will replicate users, hosts and history to this node.',
} as const;

interface JoinClusterWidgetProps {
  defaultPeerUrl?: string;
  isJoining: boolean;
  error: string | null;
  status: 'idle' | 'joining' | 'replicating';
  onBack: () => void;
  onSubmit: (peerUrl: string, token: string) => void;
}

export function JoinClusterWidget({
  defaultPeerUrl = '',
  isJoining,
  error,
  status,
  onBack,
  onSubmit,
}: JoinClusterWidgetProps) {
  const [peerUrl, setPeerUrl] = useState(defaultPeerUrl);
  const [token, setToken] = useState('');

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!peerUrl.trim() || !token.trim()) return;
    onSubmit(peerUrl.trim().replace(/\/+$/, ''), token.trim());
  };

  return (
    <form onSubmit={submit} className="space-y-5">
      <div className="space-y-1.5">
        <Label htmlFor="peer-url">Peer URL</Label>
        <Input
          id="peer-url"
          autoFocus
          placeholder="https://main.example.com"
          value={peerUrl}
          onChange={(e) => setPeerUrl(e.target.value)}
          disabled={isJoining || status === 'replicating'}
        />
        <p className="text-xs text-slate-400">
          Any node URL of the cluster. The peer will forward us to its current leader if needed.
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
