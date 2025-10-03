import { useState } from 'react';
import { Server, MoreHorizontal, RefreshCw, Square, Play } from 'lucide-react';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { getContainerStateColor } from '@/shared/lib/utils';
import { format } from 'date-fns';
import { useWidgetTheme, useSecondaryText } from '@/shared/themes';
import { useDocker } from './useDocker';

export function DockerWidget() {
  const theme = useWidgetTheme('docker');
  const secondaryTextClass = useSecondaryText();
  const { data: metrics, isLoading } = useDocker();

  if (isLoading || !metrics) {
    return (
      <div className={theme.container.className}>
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center space-x-3">
            <div className={`p-2 rounded-lg ${theme.icon.className}`}>
              <Server className="w-5 h-5" />
            </div>
            <h3 className={theme.title.className}>Docker</h3>
          </div>
          <Badge>Loading...</Badge>
        </div>
      </div>
    );
  }

  return (
    <div className={theme.container.className}>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center space-x-3">
          <div className={`p-2 rounded-lg ${theme.icon.className}`}>
            <Server className="w-5 h-5" />
          </div>
          <h3 className={theme.title.className}>Docker</h3>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-4 mb-4">
        <div className="text-center">
          <div className="text-lg font-bold text-green-400">{metrics?.latest?.running_containers ?? 0}</div>
          <div className={`text-xs ${secondaryTextClass}`}>Running</div>
        </div>
        <div className="text-center">
          <div className="text-lg font-bold text-slate-400">{(metrics?.latest?.containers?.length ?? 0) - (metrics?.latest?.running_containers ?? 0)}</div>
          <div className={`text-xs ${secondaryTextClass}`}>Stopped</div>
        </div>
        <div className="text-center">
          <div className={`text-lg font-bold ${theme.value.className.replace('text-2xl', 'text-lg')}`}>{metrics?.latest?.containers?.length ?? 0}</div>
          <div className={`text-xs ${secondaryTextClass}`}>Total</div>
        </div>
      </div>

      {theme.details.show && (
        <div className="space-y-4">
          {metrics?.latest?.containers?.map((container: any) => (
            <div
              key={container.id}
              className={`p-3 rounded-lg bg-white/5 border border-white/10 transition-all duration-200`}
            >
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center space-x-2">
                  <div
                    className="w-2 h-2 rounded-full"
                    style={{ backgroundColor: getContainerStateColor(container.state) }}
                  />
                  <div className="flex flex-col">
                    <h4 className="font-medium text-white truncate">{container.name}</h4>
                    <div className={`text-xs ${secondaryTextClass}`}>
                      {container.image}
                      {container.ports && container.ports.length > 0 && (
                        <span className="ml-2">
                          {container.ports.map((p: any) => `${p.private_port}${p.public_port ? `:${p.public_port}` : ''}`).join(', ')}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
                <div className="flex items-center space-x-1">
                  <Badge
                    variant="secondary"
                    className={`text-xs ${
                      container.state === 'running'
                        ? 'bg-green-100 text-green-700'
                        : 'bg-slate-100 text-slate-600'
                    }`}
                  >
                    {container.state}
                  </Badge>
                </div>
              </div>

              <div className="flex justify-between mb-3">
                <div className="text-center flex-1 px-2">
                  <div className="text-sm font-medium text-white">
                    {container.stats.cpu_limit > 0
                      ? `${container.stats.cpu_percent_of_limit.toFixed(1)}% / ${container.stats.cpu_limit.toFixed(1)} CPU`
                      : `${container.stats.cpu_percent_of_limit.toFixed(1)}%`
                    }
                  </div>
                  <div className={`text-xs ${secondaryTextClass}`}>CPU Usage</div>
                  <div className="w-full bg-white/10 rounded-full h-1 mt-1">
                    <div
                      className="bg-red-500 h-1 rounded-full"
                      style={{
                        width: `${Math.min(container.stats.cpu_percent_of_limit, 100)}%`
                      }}
                    />
                  </div>
                </div>
                <div className="text-center flex-1 px-2">
                  <div className="text-sm font-medium text-white">
                    {(container.stats.memory_usage / (1024 * 1024)).toFixed(1)}MB / {(container.stats.memory_limit / (1024 * 1024)).toFixed(0)}MB
                  </div>
                  <div className={`text-xs ${secondaryTextClass}`}>Memory</div>
                  <div className="w-full bg-white/10 rounded-full h-1 mt-1">
                    <div
                      className="bg-purple-500 h-1 rounded-full"
                      style={{ width: `${container.stats.memory_percent}%` }}
                    />
                  </div>
                </div>
                <div className="text-center flex-1 px-2">
                  <div className="text-sm font-medium text-white">
                    {(() => {
                      const rxKB = container.stats.network_rx / 1024;
                      const txKB = container.stats.network_tx / 1024;

                      // Show in MB if > 1024 KB, otherwise in KB
                      const rxDisplay = rxKB > 1024 ? `${(rxKB / 1024).toFixed(1)}MB` : `${rxKB.toFixed(1)}KB`;
                      const txDisplay = txKB > 1024 ? `${(txKB / 1024).toFixed(1)}MB` : `${txKB.toFixed(1)}KB`;

                      return `↓${rxDisplay} ↑${txDisplay}`;
                    })()}
                  </div>
                  <div className={`text-xs ${secondaryTextClass}`}>Network</div>
                </div>
                <div className="text-center flex-1 px-2">
                  <div className="text-sm font-medium text-white">
                    {(() => {
                      // For exited containers, show time since finished
                      // For running/restarting containers, show time since created
                      const referenceTime = container.state === 'exited' && container.finished_at
                        ? new Date(container.finished_at).getTime()
                        : new Date(container.created).getTime();

                      const uptime = Date.now() - referenceTime;
                      const days = Math.floor(uptime / (1000 * 60 * 60 * 24));
                      const hours = Math.floor((uptime % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
                      return days > 0 ? `${days}d ${hours}h` : `${hours}h`;
                    })()}
                  </div>
                  <div className={`text-xs ${secondaryTextClass}`}>
                    {container.state === 'exited' ? 'Down time' : 'Uptime'}
                  </div>
                </div>
              </div>

            </div>
          ))}
        </div>
      )}
    </div>
  );
}
