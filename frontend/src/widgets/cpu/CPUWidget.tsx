import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { Cpu } from 'lucide-react';
import { format } from 'date-fns';
import { useWidgetTheme, useSecondaryText } from '@/shared/themes';
import { useCPU } from './useCPU';

interface CPUWidgetProps {
  hostId?: number | null;
}

export function CPUWidget({ hostId }: CPUWidgetProps = {}) {
  const theme = useWidgetTheme('cpu');
  const secondaryTextClass = useSecondaryText();
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

      {theme.details.show && (
        <div className={`space-y-2 text-sm ${secondaryTextClass}`}>
          <div className="flex justify-between">
            <span>Cores:</span>
            <span>{metrics.latest?.cores ?? 'N/A'}</span>
          </div>
          <div className="flex justify-between">
            <span>Temperature:</span>
            <span>{metrics.latest?.temperature ? `${metrics.latest.temperature.toFixed(1)}°C` : 'N/A'}</span>
          </div>
        </div>
      )}

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
