import { useState } from 'react';
import { Server, Wifi, WifiOff, Clock, Zap } from 'lucide-react';
import { useHosts, useCurrentHost } from './useHosts';
import { useConnectionStatus } from '../connection-status/useConnectionStatus';
import { useWidgetTheme, useLayoutTheme, useSecondaryText } from '@/shared/themes';
import { cn } from '@/shared/lib/utils';


interface HostsWidgetProps {
  selectedHostId: number | null;
  onHostSelect: (hostId: number | null) => void;
}

// Component to display health information
function HealthInfo({ hostId, theme, secondaryTextClass }: { hostId: number, theme: any, secondaryTextClass: string }) {
  const { latency, uptime, isLoading } = useConnectionStatus(hostId);

  if (isLoading) {
    return <div className={cn("text-xs", secondaryTextClass)}>Loading...</div>;
  }

  const formatUptime = (uptimeStr: string | null) => {
    if (!uptimeStr) return '--';
    // Parse uptime string (e.g., "1h2m3.456s") and format it
    // For simplicity, just return the string as-is for now
    return uptimeStr.split('.')[0]; // Remove milliseconds
  };

  const formatLatency = (ms: number | null) => {
    if (ms === null) return '--';
    if (ms < 0) return '--';
    if (ms < 1) return '<1ms';
    return `${Math.round(ms)}ms`;
  };

  return (
    <div className="flex items-center space-x-2 text-xs">
      <div className="flex items-center space-x-1">
        <Zap className="w-3 h-3 text-yellow-400" />
        <span className={secondaryTextClass}>{formatLatency(latency)}</span>
      </div>
      <div className="flex items-center space-x-1">
        <Clock className="w-3 h-3 text-blue-400" />
        <span className={secondaryTextClass}>{formatUptime(uptime)}</span>
      </div>
    </div>
  );
}

export function HostsWidget({ selectedHostId, onHostSelect }: HostsWidgetProps) {
  const { data: hostsData, isLoading: hostsLoading } = useHosts();
  const { data: currentHostData } = useCurrentHost();
  const theme = useWidgetTheme('hosts');
  const layoutTheme = useLayoutTheme();
  const secondaryTextClass = useSecondaryText();

  const currentHost = currentHostData?.host;
  const hosts = hostsData?.hosts || [];

  return (
    <div className="space-y-4">
      <h3 className={theme.title.className}>Hosts</h3>

      {/* All Hosts Button */}
      <div
        className={cn(
          layoutTheme.hostItem.className,
          selectedHostId === null && layoutTheme.hostItem.selectedClassName,
          "flex items-center space-x-3 w-full"
        )}
        onClick={() => onHostSelect(null)}
      >
        <Server className="w-4 h-4" />
        <span className="font-medium">All Hosts</span>
      </div>

      {/* Hosts List */}
      <div className="space-y-2">
        {hostsLoading ? (
          <div className={cn("text-center py-4", secondaryTextClass)}>Loading hosts...</div>
        ) : hosts.length === 0 ? (
          <div className={cn("text-center py-4", secondaryTextClass)}>No hosts found</div>
        ) : (
          hosts.map((host: any) => {
            const isSelected = selectedHostId === host.id;
            const isCurrent = host.id === currentHost?.id;

            return (
              <div
                key={host.id}
                className={cn(
                  layoutTheme.hostItem.className,
                  isSelected && layoutTheme.hostItem.selectedClassName,
                  isCurrent && "ring-2 ring-green-400/50",
                  "flex items-center space-x-3 w-full"
                )}
                onClick={() => onHostSelect(host.id)}
              >
                <div className="flex-shrink-0">
                  <Wifi className="w-4 h-4 text-green-400" />
                </div>
                <div className="flex-1 min-w-0 text-left">
                  <div className={cn("font-medium truncate", theme.value.className)}>{host.name}</div>
                  <div className={cn("text-xs truncate", secondaryTextClass)}>{host.mac_address}</div>
                  <HealthInfo hostId={host.id} theme={theme} secondaryTextClass={secondaryTextClass} />
                  {isCurrent && (
                    <div className="text-xs text-green-400">Current Host</div>
                  )}
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
