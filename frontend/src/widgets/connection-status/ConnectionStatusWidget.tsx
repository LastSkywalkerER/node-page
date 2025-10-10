import { cn } from '@/shared/lib/utils';
import { useConnectionStatus } from './useConnectionStatus';
import { useHost } from '@/shared/lib/HostContext';

export default function ConnectionStatusWidget() {
  const { selectedHostId } = useHost();
  const { isConnected, latency, uptime } = useConnectionStatus(selectedHostId || undefined);

  const formatUptime = (uptimeStr: string | null) => {
    if (!uptimeStr) return '--';
    // Remove milliseconds and return formatted uptime
    return uptimeStr.split('.')[0];
  };

  const formatLatency = (ms: number | null) => {
    if (ms === null) return '--';
    if (ms < 0) return '--';
    if (ms < 1) return '<1ms';
    return `${Math.round(ms)}ms`;
  };

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
          latency: {formatLatency(latency)}
        </span>
      )}
      {uptime && isConnected && (
        <span className="text-xs text-white/60">
          uptime: {formatUptime(uptime)}
        </span>
      )}
    </div>
  );
}
