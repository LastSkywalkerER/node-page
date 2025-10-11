import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { Cpu } from 'lucide-react';
import { format } from 'date-fns';
import { useWidgetTheme } from '@/shared/themes';
import { useCPU } from './useCPU';

interface CPUWidgetProps {
  hostId?: number | null;
}

export function CPUWidget({ hostId }: CPUWidgetProps = {}) {
  const theme = useWidgetTheme('cpu');
  const { data: metrics, isLoading } = useCPU(hostId);

  if (isLoading || !metrics) {
    return (
      <div className={theme.container.className}>
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center space-x-3">
            <div className={`p-2 rounded-lg ${theme.icon.className}`}>
              <Cpu className="w-5 h-5" />
            </div>
            <h3 className={`text-lg font-semibold ${theme.title.className}`}>CPU</h3>
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
            <Cpu className="w-5 h-5" />
          </div>
          <h3 className={`text-lg font-semibold ${theme.title.className}`}>CPU</h3>
        </div>
        <div className="text-right">
          <div className={theme.value.className}>
            {metrics.latest?.usage_percent ? `${metrics.latest.usage_percent.toFixed(1)}%` : 'N/A'}
          </div>
        </div>
      </div>

      {theme.details.show && (() => {
        const latest = metrics.latest ?? {} as any;

        const labelMap: Record<string, string> = {
          usage_percent: 'Usage',
          cores: 'Cores',
          model_name: 'Model',
          vendor_id: 'Vendor',
          mhz: 'Base Clock',
          temperature: 'Temperature',
          load_avg_1: 'Load avg (1m)',
          load_avg_5: 'Load avg (5m)',
          load_avg_15: 'Load avg (15m)',
          cache_size: 'Cache size',
          family: 'Family',
          model: 'Model ID',
          flags: 'Flags',
          microcode: 'Microcode',
          user: 'User time',
          system: 'System time',
          idle: 'Idle time',
          nice: 'Nice time',
          iowait: 'IO wait',
          irq: 'IRQ',
          softirq: 'SoftIRQ',
          steal: 'Steal',
          guest: 'Guest',
          guest_nice: 'Guest nice',
        };

        const isEmptyValue = (value: unknown): boolean => {
          if (value === null || value === undefined) return true;
          if (typeof value === 'number') return value === 0;
          if (typeof value === 'string') return value.trim().length === 0;
          if (Array.isArray(value)) return value.length === 0;
          return false;
        };

        const humanizeKey = (key: string): string =>
          key
            .replace(/_/g, ' ')
            .replace(/\b\w/g, (c) => c.toUpperCase());

        const formatValue = (key: string, value: unknown): string => {
          if (value === null || value === undefined) return 'N/A';
          if (key === 'mhz' && typeof value === 'number') return `${value.toFixed(0)} MHz`;
          if (key === 'temperature' && typeof value === 'number') return `${value.toFixed(1)}°C`;
          if (key.startsWith('load_avg_') && typeof value === 'number') return value.toFixed(2);
          if (key === 'flags' && Array.isArray(value)) return value.join(', ');
          if (typeof value === 'number') return String(value);
          return String(value);
        };

        const entries = Object.entries(latest)
          .filter(([key, value]) => key !== 'usage_percent' && !isEmptyValue(value))
          .map(([key, value]) => ({
            key,
            label: labelMap[key] ?? humanizeKey(key),
            value: formatValue(key, value),
          }));

        if (entries.length === 0) return null;

        return (
          <div className="space-y-1 text-xs opacity-60">
            {entries.map((entry) => (
              <div className="flex justify-between" key={entry.key}>
                <span>{entry.label}:</span>
                <span className="truncate max-w-[60%] text-right">{entry.value}</span>
              </div>
            ))}
          </div>
        );
      })()}

      {theme.details.show && metrics?.history && metrics.history.length > 0 && (
        <div className="mt-4">
          <div className="h-32">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={metrics.history.map((point: any) => ({
                time: format(new Date(point.timestamp), 'HH:mm'),
                usage: point.usage,
                load1: point.load_avg_1,
                load5: point.load_avg_5,
                load15: point.load_avg_15,
              }))}>
                <defs>
                  <linearGradient id="cpuGradient" x1="0" y1="0" x2="0" y2="1">
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
                  fill="url(#cpuGradient)"
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
