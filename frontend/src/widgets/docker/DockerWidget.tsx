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
  const [selectedContainer, setSelectedContainer] = useState<string | null>(null);

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
              className={`p-3 rounded-lg bg-white/5 border border-white/10 transition-all duration-200 ${
                selectedContainer === container.id ? 'ring-2 ring-blue-500/50' : ''
              }`}
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
                  <Button
                    variant="ghost"
                    size="sm"
                    className={`h-6 w-6 p-0 ${theme.container.className} hover:opacity-70`}
                    onClick={() => setSelectedContainer(
                      selectedContainer === container.id ? null : container.id
                    )}
                  >
                    <MoreHorizontal className="w-3 h-3" />
                  </Button>
                </div>
              </div>

              <div className="flex justify-between mb-3">
                <div className="text-center flex-1 px-2">
                  <div className="text-sm font-medium text-white">
                    {container.stats.cpu_limit > 0
                      ? `${container.stats.cpu_percent_of_limit.toFixed(1)}% / ${container.stats.cpu_limit.toFixed(1)} CPU`
                      : `${container.stats.cpu_percent_of_limit.toFixed(1)}% of available CPU`
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
                    ↓{(container.stats.network_rx / (1024 * 1024)).toFixed(1)}MB ↑{(container.stats.network_tx / (1024 * 1024)).toFixed(1)}MB
                  </div>
                  <div className={`text-xs ${secondaryTextClass}`}>Network</div>
                </div>
                <div className="text-center flex-1 px-2">
                  <div className="text-sm font-medium text-white">
                    {(() => {
                      const uptime = Date.now() - new Date(container.created).getTime();
                      const days = Math.floor(uptime / (1000 * 60 * 60 * 24));
                      const hours = Math.floor((uptime % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
                      return days > 0 ? `${days}d ${hours}h` : `${hours}h`;
                    })()}
                  </div>
                  <div className={`text-xs ${secondaryTextClass}`}>Uptime</div>
                </div>
              </div>

              {selectedContainer === container.id && (
                <div className="border-t border-white/10 pt-3 mt-3 space-y-2">
                  <div className="flex justify-between items-center">
                    <Button variant="outline" size="sm" className="flex-1 mr-2">
                      <RefreshCw className="w-3 h-3 mr-1" />
                      Restart
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      className="flex-1"
                      disabled={container.state !== 'running'}
                    >
                      {container.state === 'running' ? (
                        <>
                          <Square className="w-3 h-3 mr-1" />
                          Stop
                        </>
                      ) : (
                        <>
                          <Play className="w-3 h-3 mr-1" />
                          Start
                        </>
                      )}
                    </Button>
                  </div>
                  <div className={`text-xs ${secondaryTextClass} space-y-1`}>
                    <div>Container: {container.name}</div>
                    <div>Image: {container.image}</div>
                    <div>Status: {container.status}</div>
                    <div>Ports: {container.ports.length > 0 ? container.ports.map((p: any) => `${p.private_port}:${p.public_port}`).join(', ') : 'None'}</div>
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
