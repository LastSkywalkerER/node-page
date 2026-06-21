import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { authService } from '@/shared/lib/auth';
import { useIssueRaftJoinToken, useRaftStatus } from '@/widgets/raft/useRaft';
import { useSetupStatus } from './useSetup';
import { copyToClipboard } from '@/shared/lib/clipboard';

export const SUCCESS_STEP_META = {
  title: 'Setup Complete',
  description: 'Setup completed successfully!',
} as const;

interface SuccessWidgetProps {
  /** True when the operator enabled cluster sync — show the connect-key panel. */
  raftEnabled?: boolean;
  /** Admin credentials just created, used to auto-login so we can issue a
   *  join token without sending the user to the login screen first. */
  adminEmail?: string;
  adminPassword?: string;
}

function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={async () => {
        if (await copyToClipboard(value)) {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        }
      }}
      disabled={!value}
    >
      {copied ? 'Copied' : label}
    </Button>
  );
}

export function SuccessWidget({ raftEnabled, adminEmail, adminPassword }: SuccessWidgetProps) {
  const navigate = useNavigate();
  const [loggedIn, setLoggedIn] = useState(false);
  const [loginError, setLoginError] = useState<string | null>(null);
  const loginAttempted = useRef(false);

  const issueToken = useIssueRaftJoinToken();
  // Once logged in, read this node's own Raft view to surface the URL peers
  // should dial (its advertise URL), falling back to the browser URL.
  const { data: raftStatus } = useRaftStatus(raftEnabled === true && loggedIn);

  // Auto-login with the just-created admin so the admin-only join-token
  // endpoint accepts our request. Runs once.
  useEffect(() => {
    if (!raftEnabled || loginAttempted.current) return;
    if (!adminEmail || !adminPassword) return;
    loginAttempted.current = true;
    authService
      .login(adminEmail, adminPassword)
      .then(() => setLoggedIn(true))
      .catch((e: unknown) => setLoginError(e instanceof Error ? e.message : 'login failed'));
  }, [raftEnabled, adminEmail, adminPassword]);

  // Auto-issue the first connect key as soon as we're logged in, so the key
  // is visible without an extra click. Guarded to fire once.
  const tokenRequested = useRef(false);
  useEffect(() => {
    if (!loggedIn || tokenRequested.current) return;
    tokenRequested.current = true;
    issueToken.mutate({ ttl_minutes: 60 });
  }, [loggedIn, issueToken]);

  const token = issueToken.data?.token ?? '';
  // The URL peers should dial: prefer this node's Raft advertise URL; else build
  // one from the detected host IPv4 (so it's routable from other machines);
  // only fall back to the browser origin (localhost) as a last resort.
  const { data: setupStatus } = useSetupStatus();
  const advertiseUrl = raftStatus?.status?.advertise_url?.trim() || '';
  const hintIp = setupStatus?.machine_hints?.suggested_ipv4?.trim() || '';
  const portSuffix = window.location.port ? `:${window.location.port}` : '';
  const ipOrigin = hintIp ? `${window.location.protocol}//${hintIp}${portSuffix}` : '';
  const peerUrl = advertiseUrl || ipOrigin || window.location.origin;

  // Live list of OTHER nodes that have joined this cluster — the local node
  // (status.node_id) is filtered out. useRaftStatus polls every 5s, so a node
  // that completes the join wizard shows up here within a few seconds.
  const selfNodeId = raftStatus?.status?.node_id || '';
  const otherPeers = (raftStatus?.status?.peers || []).filter((p) => p.id !== selfNodeId);

  if (!raftEnabled) {
    return (
      <div className="space-y-6">
        <Alert className="bg-green-900/50 border-green-700">
          <AlertDescription className="text-green-200">
            Setup completed successfully!
          </AlertDescription>
        </Alert>
        <Button onClick={() => navigate('/auth')} className="w-full">
          Go to Login
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <Alert className="bg-green-900/50 border-green-700">
        <AlertDescription className="text-green-200">
          Cluster created — this node is the leader. Add another node below.
        </AlertDescription>
      </Alert>

      <div className="rounded-md border border-border/60 bg-muted/10 p-4 space-y-4">
        <div>
          <h3 className="text-sm font-semibold">Add another node</h3>
          <p className="text-xs text-slate-400">
            On the new node's setup screen choose <strong>Join an existing cluster</strong>,
            then paste the connect key (and the URL if asked).
          </p>
        </div>

        <div className="space-y-1.5">
          <Label>Connect key</Label>
          {loginError ? (
            <p className="text-xs text-rose-300">
              Couldn't auto-login to issue a key: {loginError}. You can generate one later
              under <code>Admin → Nodes</code>.
            </p>
          ) : !loggedIn ? (
            <p className="text-xs text-slate-400">Preparing…</p>
          ) : issueToken.isError ? (
            <p className="text-xs text-rose-300">
              Failed to generate a key: {(issueToken.error as Error)?.message}
            </p>
          ) : (
            <div className="flex gap-2">
              <Input
                readOnly
                value={issueToken.isPending ? 'Generating…' : token}
                className="font-mono text-xs"
              />
              <CopyButton value={token} />
            </div>
          )}
          <p className="text-xs text-slate-400">One-shot, expires in 60 min.</p>
        </div>

        <div className="space-y-1.5">
          <Label>Cluster node URL</Label>
          <div className="flex gap-2">
            <Input readOnly value={peerUrl} className="font-mono text-xs" />
            <CopyButton value={peerUrl} />
          </div>
          <p className="text-xs text-slate-400">
            The new node must be able to reach this URL. If they're on different
            hosts/networks, use an address that resolves to this node from the new one.
          </p>
        </div>

        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!loggedIn || issueToken.isPending}
            onClick={() => issueToken.mutate({ ttl_minutes: 60 })}
          >
            {issueToken.isPending ? 'Generating…' : token ? 'Regenerate key' : 'Generate key'}
          </Button>
        </div>
      </div>

      <div className="rounded-md border border-border/60 bg-muted/10 p-4 space-y-2">
        <h3 className="text-sm font-semibold">
          Nodes connected
          {otherPeers.length > 0 && (
            <span className="ml-2 font-mono text-xs font-normal text-muted-foreground">
              ({otherPeers.length})
            </span>
          )}
        </h3>
        {otherPeers.length === 0 ? (
          <p className="text-xs text-slate-400">
            Waiting for another node to join… it appears here within a few seconds of completing
            the join wizard.
          </p>
        ) : (
          <ul className="space-y-1">
            {otherPeers.map((p) => (
              <li key={p.id} className="flex items-center gap-2 text-sm">
                <span className="text-emerald-400">✓</span>
                <span className="font-mono">{p.id}</span>
                <span className="font-mono text-xs text-slate-400">{p.addr}</span>
                {p.suffrage !== 'voter' && (
                  <span className="text-xs text-amber-300">({p.suffrage})</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>

      <Button onClick={() => navigate('/auth')} className="w-full">
        Go to dashboard / login
      </Button>
    </div>
  );
}
