import { cn } from '@/shared/lib/utils';
import { useConnectionStatus } from './useConnectionStatus';

export default function ConnectionStatusWidget() {
  const { isConnected, latency, uptime } = useConnectionStatus();

  return (
    <div className="flex items-center space-x-2">
      <div
        className={cn(
          'h-2 w-2 rounded-full',
          isConnected ? 'bg-green-500 animate-pulse' : 'bg-red-500'
        )}
      />
      <span className="text-sm font-medium">
        {isConnected ? 'Connected' : 'Disconnected'}
      </span>
      {latency !== null && isConnected && (
        <span className="text-xs text-white/60">
          latancy: {latency}ms
        </span>
      )}
      {uptime && isConnected && (
        <span className="text-xs text-white/60">
          uptime: {uptime}
        </span>
      )}
    </div>
  );
}
