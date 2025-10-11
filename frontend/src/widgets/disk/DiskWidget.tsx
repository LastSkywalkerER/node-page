import { HardDrive } from 'lucide-react';
import { formatBytes } from '@/shared/lib/utils';
import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { format } from 'date-fns';
import { useWidgetTheme } from '@/shared/themes';
import { useDisk } from './useDisk';

interface DiskWidgetProps {
  hostId?: number | null;
}

export function DiskWidget({ hostId }: DiskWidgetProps = {}) {
  const theme = useWidgetTheme('disk');
  const { data: metrics, isLoading } = useDisk(hostId);
  const latest = (metrics as any)?.latest || {};
  const mounts: any[] = Array.isArray(latest?.mounts) ? latest.mounts : [];
  const partitions: any[] = Array.isArray(latest?.partitions) ? latest.partitions : [];
  const ioCounters: any[] = Array.isArray(latest?.io_counters) ? latest.io_counters : [];
  const topReadDev = ioCounters.reduce((best: any, d: any) => (best && best.read_bytes > d.read_bytes ? best : d), null as any);
  const topWriteDev = ioCounters.reduce((best: any, d: any) => (best && best.write_bytes > d.write_bytes ? best : d), null as any);

  if (isLoading || !metrics) {
    return (
      <div className={theme.container.className}>
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center space-x-3">
            <div className={`p-2 rounded-lg ${theme.icon.className}`}>
              <HardDrive className="w-5 h-5" />
            </div>
            <h3 className={`text-lg font-semibold ${theme.title.className}`}>Disk</h3>
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
            <HardDrive className="w-5 h-5" />
          </div>
          <h3 className={`text-lg font-semibold ${theme.title.className}`}>Disk</h3>
        </div>
        <div className="text-right">
          <div className={theme.value.className}>
            {metrics.latest?.usage_percent ? `${metrics.latest.usage_percent.toFixed(1)}%` : 'N/A'}
          </div>
        </div>
      </div>

      {theme.details.show && (
        <div className="space-y-1 text-xs opacity-60">
          <div className="flex justify-between">
            <span>Used:</span>
            <span>{metrics.latest?.used ? formatBytes(metrics.latest.used) : 'N/A'}</span>
          </div>
          <div className="flex justify-between">
            <span>Free:</span>
            <span>{metrics.latest?.free ? formatBytes(metrics.latest.free) : 'N/A'}</span>
          </div>
          <div className="space-y-1">
            <div className="flex justify-between">
              <span>Partitions:</span>
              <span>{partitions.length}</span>
            </div>
            {mounts
              .slice()
              .sort((a: any, b: any) => b.used_percent - a.used_percent)
              .slice(0, 3)
              .map((m: any) => (
                <div key={m.path} className="flex justify-between">
                  <span className="truncate max-w-[50%]" title={m.path}>{m.path} ({m.fstype})</span>
                  <span>{m.used_percent.toFixed(1)}% · {formatBytes(m.used)}/{formatBytes(m.total)}</span>
                </div>
              ))}
          </div>

          {partitions.slice(0, 2).map((p: any) => (
            <div key={`${p.device}-${p.mountpoint}`} className="flex justify-between">
              <span className="truncate max-w-[60%]" title={p.device}>{p.device} → {p.mountpoint}</span>
              <span>{p.fstype}{p.opts ? ` • ${p.opts}` : ''}</span>
            </div>
          ))}

          {ioCounters.length > 0 && (
            <div className="space-y-1 text-xs opacity-60">
              <div className="flex justify-between">
                <span>Devices:</span>
                <span>{ioCounters.length}</span>
              </div>
              {topReadDev && (
                <div className="flex justify-between">
                  <span>Top Read:</span>
                  <span>{topReadDev.name} · {formatBytes(topReadDev.read_bytes)}</span>
                </div>
              )}
              {topWriteDev && (
                <div className="flex justify-between">
                  <span>Top Write:</span>
                  <span>{topWriteDev.name} · {formatBytes(topWriteDev.write_bytes)}</span>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {theme.details.show && metrics?.history && metrics.history.length > 0 && (
        <div className="mt-4">
          <div className="h-32">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={metrics.history.map((point: any) => ({
                time: format(new Date(point.timestamp), 'HH:mm'),
                usage: point.usage_percent,
                used: point.used_bytes,
                total: point.total_bytes,
              }))}>
                <defs>
                  <linearGradient id="diskGradient" x1="0" y1="0" x2="0" y2="1">
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
                  domain={[0, 'dataMax']}
                  tickFormatter={(value) => formatBytes(value)}
                />
                <Area
                  type="monotone"
                  dataKey="used"
                  stroke={theme.chart.color}
                  fillOpacity={1}
                  fill="url(#diskGradient)"
                  strokeWidth={2}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}
    </div>
  );
}
