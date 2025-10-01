import { useDashboardStore } from '@/shared/lib/store';
import type { Alert } from '@/shared/types/metrics';
import { Button } from '@/shared/ui/button';
import { X, AlertCircle, AlertTriangle, Info } from 'lucide-react';
import { format } from 'date-fns';

interface AlertsPanelProps {
  alerts: Alert[];
}

const alertIcons = {
  info: Info,
  warn: AlertTriangle,
  crit: AlertCircle,
};

const alertColors = {
  info: 'text-blue-400 bg-blue-500/10 border-blue-500/20',
  warn: 'text-yellow-400 bg-yellow-500/10 border-yellow-500/20',
  crit: 'text-red-400 bg-red-500/10 border-red-500/20',
};

export default function AlertsPanel({ alerts }: AlertsPanelProps) {
  const removeAlert = useDashboardStore((state) => state.removeAlert);
  const clearAlerts = useDashboardStore((state) => state.clearAlerts);

  if (alerts.length === 0) return null;

  return (
    <div className="rounded-lg border border-white/10 bg-white/5 p-4">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-medium text-white">Alerts & Events</h3>
        <Button
          variant="ghost"
          size="sm"
          onClick={clearAlerts}
          className="text-xs text-white/60 hover:text-white"
        >
          Clear all
        </Button>
      </div>

      <div className="space-y-2 max-h-48 overflow-y-auto">
        {alerts.map((alert) => {
          const Icon = alertIcons[alert.level];
          const colorClass = alertColors[alert.level];

          return (
            <div
              key={alert.id}
              className={`flex items-start space-x-3 rounded-md border p-3 ${colorClass}`}
            >
              <Icon className="mt-0.5 h-4 w-4 flex-shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="text-sm">{alert.message}</p>
                <p className="text-xs text-white/60 mt-1">
                  {format(new Date(alert.timestamp), 'HH:mm:ss')}
                  {alert.metric && ` • ${alert.metric}`}
                  {alert.value !== undefined && ` • ${alert.value.toFixed(1)}`}
                </p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => removeAlert(alert.id)}
                className="h-6 w-6 p-0 text-white/60 hover:text-white"
              >
                <X className="h-3 w-3" />
              </Button>
            </div>
          );
        })}
      </div>
    </div>
  );
}
