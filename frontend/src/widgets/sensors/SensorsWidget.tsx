import { Thermometer } from 'lucide-react';
import { useWidgetTheme } from '@/shared/themes';
import { useSensors } from './useSensors';
import { useHost } from '@/shared/lib/HostContext';
import { TemperatureStat } from './schemas';

export default function SensorsWidget() {
  const theme = useWidgetTheme('sensors');
  const { selectedHostId } = useHost();
  const { data, isLoading } = useSensors(selectedHostId);

  if (isLoading || !data) {
    return (
      <div className={theme.container.className}>
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center space-x-3">
            <div className={`p-2 rounded-lg ${theme.icon.className}`}>
              <Thermometer className="w-5 h-5" />
            </div>
            <h3 className={`text-lg font-semibold ${theme.title.className}`}>Sensors</h3>
          </div>
          <div className="text-right">
            <div className={theme.value.className}>Loading...</div>
          </div>
        </div>
      </div>
    );
  }

  const sensors: TemperatureStat[] = data?.sensors?.sensors || [];
  const hottest = sensors.reduce((max, s) => (s.temperature > max.temperature ? s : max), sensors[0] || { sensor_key: '', temperature: 0, high: 0, critical: 0 });

  return (
    <div className={theme.container.className}>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center space-x-3">
          <div className={`p-2 rounded-lg ${theme.icon.className}`}>
            <Thermometer className="w-5 h-5" />
          </div>
          <h3 className={`text-lg font-semibold ${theme.title.className}`}>Sensors</h3>
        </div>
        <div className="text-right">
          <div className={theme.value.className}>{hottest?.temperature ? `${hottest.temperature.toFixed(1)}°C` : 'N/A'}</div>
        </div>
      </div>

      {theme.details.show && (
        <div className="space-y-1 text-xs opacity-60">
          {sensors.slice(0, 8).map((s) => (
            <div key={s.sensor_key} className="flex items-center justify-between">
              <span className="truncate max-w-[60%]" title={s.sensor_key}>{s.sensor_key}</span>
              <span>
                {s.temperature.toFixed(1)}°C
                {s.high ? ` / ${s.high.toFixed(1)}°C` : ''}
                {s.critical ? ` / ${s.critical.toFixed(1)}°C` : ''}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}


