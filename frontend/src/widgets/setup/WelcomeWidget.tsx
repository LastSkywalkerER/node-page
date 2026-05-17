import { Button } from '@/components/ui/button';

export const WELCOME_STEP_META = {
  title: 'Welcome',
  description: 'How would you like to set up this node?',
} as const;

interface WelcomeWidgetProps {
  onStartNewCluster: () => void;
  onJoinExistingCluster: () => void;
}

export function WelcomeWidget({ onStartNewCluster, onJoinExistingCluster }: WelcomeWidgetProps) {
  return (
    <div className="space-y-6">
      <p className="text-slate-300">
        Welcome to System Stats! Pick how this node should join the network.
      </p>

      <div className="grid gap-3">
        <Button onClick={onStartNewCluster} className="w-full justify-start text-left h-auto py-3">
          <div className="flex flex-col items-start">
            <span className="font-semibold">Start a new cluster</span>
            <span className="text-xs text-slate-400 normal-case">
              Configure this server, create the first admin user. Optionally bootstrap a Raft cluster.
            </span>
          </div>
        </Button>

        <Button
          onClick={onJoinExistingCluster}
          variant="outline"
          className="w-full justify-start text-left h-auto py-3"
        >
          <div className="flex flex-col items-start">
            <span className="font-semibold">Join an existing cluster</span>
            <span className="text-xs text-slate-400 normal-case">
              Paste a peer URL and a one-shot join token. The cluster will replicate users, hosts and history to this node.
            </span>
          </div>
        </Button>
      </div>
    </div>
  );
}

