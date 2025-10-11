import { Network } from 'lucide-react';
import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { format } from 'date-fns';
import { useWidgetTheme } from '@/shared/themes';
import { useNetwork } from './useNetwork';
import { NetworkInterface } from './schemas';

// Utility functions
const formatSpeed = (kbps: number): string => {
  if (kbps >= 1024 * 1024) {
    return `${(kbps / (1024 * 1024)).toFixed(1)} GB/s`;
  } else if (kbps >= 1024) {
    return `${(kbps / 1024).toFixed(1)} MB/s`;
  } else {
    return `${kbps.toFixed(0)} KB/s`;
  }
};

const formatBytes = (bytes: number): string => {
  if (bytes >= 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  } else if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  } else if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  } else {
    return `${bytes} B`;
  }
};

const getMaxSpeed = (iface: NetworkInterface): number => {
  return Math.max(iface.speed_kbps_sent, iface.speed_kbps_recv);
};

const getTotalTraffic = (iface: NetworkInterface): number => {
  return iface.bytes_sent + iface.bytes_recv;
};

const hasActiveSpeed = (iface: NetworkInterface): boolean => {
  return iface.speed_kbps_sent > 0 || iface.speed_kbps_recv > 0;
};

const getPrimaryInterface = (interfaces: NetworkInterface[]): NetworkInterface | null => {
  if (!interfaces.length) return null;
  const primary = interfaces.find((i) => (i as any).is_primary === true);
  if (primary) return primary;
  // Fallback to the one with most total traffic if no primary is provided
  return interfaces.reduce((best, current) =>
    getTotalTraffic(current) > getTotalTraffic(best) ? current : best
  );
};

const getPrimaryIp = (iface?: NetworkInterface | null): string | null => {
  if (!iface) return null;
  const ips = (iface as any).ips as string[] | undefined;
  if (!ips || ips.length === 0) return null;
  // Prefer IPv4 if available, otherwise first
  const ipv4 = ips.find(ip => ip.includes("."));
  return ipv4 || ips[0];
};

const getMac = (iface?: NetworkInterface | null): string | null => {
  if (!iface) return null;
  const mac = (iface as any).mac as string | undefined;
  return mac && mac.length > 0 ? mac : null;
};

interface NetworkWidgetProps {
  hostId?: number | null;
}

export function NetworkWidget({ hostId }: NetworkWidgetProps = {}) {
  const theme = useWidgetTheme('network');
  const { data: metrics, isLoading } = useNetwork(hostId);

  // Process network data
  const interfaces = metrics?.latest?.interfaces || [];
  const fastestInterface = getPrimaryInterface(interfaces);
  const activeInterfaces = interfaces.filter((iface: NetworkInterface) => hasActiveSpeed(iface));
  const inactiveInterfaces = interfaces.filter((iface: NetworkInterface) => !hasActiveSpeed(iface));

  if (isLoading || !metrics) {
    return (
      <div className={theme.container.className}>
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center space-x-3">
            <div className={`p-2 rounded-lg ${theme.icon.className}`}>
              <Network className="w-5 h-5" />
            </div>
            <h3 className={`text-lg font-semibold ${theme.title.className}`}>Network</h3>
          </div>
          <div className="text-right">
            <div className={theme.value.className}>Loading...</div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className={theme.container.className}>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center space-x-3">
          <div className={`p-2 rounded-lg ${theme.icon.className}`}>
            <Network className="w-5 h-5" />
          </div>
          <h3 className={`text-lg font-semibold ${theme.title.className}`}>Network</h3>
        </div>
        <div className="text-right">
          <div className={theme.value.className}>
            {getPrimaryIp(fastestInterface)}
          </div>
          <div className={theme.value.className}>
              {getMac(fastestInterface)}
          </div>
        </div>
      </div>

      {theme.details.show && (
        <div className="space-y-1 text-xs opacity-60">
          {/* Fastest interface traffic details */}
          {fastestInterface && (
            <div className="space-y-1">
              <div className="flex justify-between">
                <span>{fastestInterface.name}:</span>
                <div className="text-right text-xs">
                  <div>↑ {formatBytes(fastestInterface.bytes_sent)} ↑ {formatSpeed(fastestInterface.speed_kbps_sent)}</div>
                  <div> ↓ {formatBytes(fastestInterface.bytes_recv)} ↓ {formatSpeed(fastestInterface.speed_kbps_recv)}</div>
                </div>
              </div>
            </div>
          )}

          {/* Other active interfaces with speeds */}
          {activeInterfaces.filter((iface: NetworkInterface) => iface.name !== fastestInterface?.name).map((iface: NetworkInterface) => (
            <div key={iface.name} className="flex justify-between">
              <span>{iface.name}:</span>
              <div className="text-right text-xs">
                  <div>MAC: {(iface as any).mac || ''} ↑ {formatSpeed(iface.speed_kbps_sent)}</div>
                  <div>IP: {getPrimaryIp(iface)} ↓ {formatSpeed(iface.speed_kbps_recv)}</div>
                </div>
            </div>
          ))}

          {/* Inactive interfaces as comma-separated list */}
          {inactiveInterfaces.length > 0 && (
            <div className="flex justify-between">
              <span>Inactive:</span>
              <span className="text-right">
                {inactiveInterfaces.map((iface: NetworkInterface) => iface.name).join(', ')}
              </span>
            </div>
          )}

          {/* Error/Drop counters for primary interface if available */}
          {!!fastestInterface && (!!fastestInterface.errin || !!fastestInterface.errout || !!fastestInterface.dropin || !!fastestInterface.dropout) && (
            <div className="flex justify-between text-xs opacity-60">
              <span>Errors/Drops (pkts):</span>
              <span className="text-right">
                err-in:{fastestInterface.errin || 0} · err-out:{fastestInterface.errout || 0} · drop-in:{fastestInterface.dropin || 0} · drop-out:{fastestInterface.dropout || 0}
              </span>
            </div>
          )}
        </div>
      )}

      {theme.details.show && metrics?.history && metrics.history.length > 0 && fastestInterface && (
        <div className="mt-4">
          <div className="h-32">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={metrics.history
                .slice(-20)
                .map((point: any) => {
                  const iface = point.interfaces?.find((i: any) => i.name === fastestInterface.name);
                  return {
                    time: 'timestamp' in point ? format(new Date(point.timestamp), 'HH:mm') : format(new Date(), 'HH:mm'),
                    speed: iface ? iface.speed_kbps_sent / 1000 : 0, // Convert to Mbps
                  };
                })}
              >
                <defs>
                  <linearGradient id="networkGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={theme.chart.color} stopOpacity={0.3}/>
                    <stop offset="95%" stopColor={theme.chart.color} stopOpacity={0}/>
                  </linearGradient>
                </defs>
                <XAxis
                  dataKey="time"
                  axisLine={false}
                  tickLine={false}
                  tick={{fontSize: 10, fill: 'currentColor', opacity: 0.6}}
                />
                <YAxis
                  axisLine={false}
                  tickLine={false}
                  tick={{fontSize: 10, fill: 'currentColor', opacity: 0.6}}
                />
                <Area
                  type="monotone"
                  dataKey="speed"
                  stroke={theme.chart.color}
                  fillOpacity={1}
                  fill="url(#networkGradient)"
                  strokeWidth={2}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
          <div className="text-xs opacity-60 mt-1">Speed (Mbps) - {fastestInterface.name}</div>
        </div>
      )}
    </div>
  );
}
