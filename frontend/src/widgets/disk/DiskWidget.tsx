import { HardDrive } from 'lucide-react';
import { formatBytes } from '@/shared/lib/utils';
import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { format } from 'date-fns';
import { useWidgetTheme } from '@/shared/themes';
import { useDisk } from './useDisk';

export function DiskWidget() {
  const theme = useWidgetTheme('disk');
  const { data: metrics, isLoading } = useDisk();

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
        <div className={`space-y-2 text-sm ${theme.details.className || ''}`}>
          <div className="flex justify-between">
            <span>Used:</span>
            <span>{metrics.latest?.used ? formatBytes(metrics.latest.used) : 'N/A'}</span>
          </div>
          <div className="flex justify-between">
            <span>Free:</span>
            <span>{metrics.latest?.free ? formatBytes(metrics.latest.free) : 'N/A'}</span>
          </div>
          <div className="flex justify-between">
            <span>Partitions:</span>
            <span>3</span>
          </div>
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
                  domain={[0, 100]}
                />
                <Area
                  type="monotone"
                  dataKey="usage"
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
