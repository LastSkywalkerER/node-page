import { ReactNode, useState, useEffect } from 'react';
import { useAlerts } from '@/shared/lib/store';
import { HostProvider } from '@/shared/lib/HostContext';
import ConnectionStatusWidget from '@/widgets/connection-status/ConnectionStatusWidget';
import AlertsPanel from './components/AlertsPanel';
import { HostsSidebar } from './components/HostsSidebar';
import { useCurrentHost, useHosts } from '@/widgets/hosts/useHosts';

interface DashboardProps {
  children: ReactNode;
}

export default function Dashboard({ children }: DashboardProps) {
  const alerts = useAlerts();
  const [selectedHostId, setSelectedHostId] = useState<number | null>(null);
  const { data: currentHostData, isLoading: currentHostLoading } = useCurrentHost();
  const { data: hostsData } = useHosts();

  // Auto-select current host on first load
  useEffect(() => {
    if (!currentHostLoading && currentHostData?.host && selectedHostId === null) {
      setSelectedHostId(currentHostData.host.id);
    }
  }, [currentHostData, selectedHostId, currentHostLoading]);

  // Find selected host name
  const selectedHostName = hostsData?.hosts?.find((host: any) => host.id === selectedHostId)?.name;

  return (
    <HostProvider selectedHostId={selectedHostId} setSelectedHostId={setSelectedHostId}>
      <div className="min-h-screen flex">
        {/* Main Content Area */}
        <div className="flex-1 flex flex-col">
          {/* Top Bar */}
          <div className="sticky top-0 z-40 border-b border-white/10 bg-black/20 backdrop-blur-xl">
            <div className="mx-auto max-w-[1600px] px-6 py-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center space-x-6">
                  <ConnectionStatusWidget />
                  {selectedHostId && selectedHostName && (
                    <div className="text-sm text-white/60">
                      Host: {selectedHostName}
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>

          {/* Alerts */}
          {alerts.length > 0 && (
            <div className="mx-auto max-w-[1600px] px-6 py-4">
              <AlertsPanel alerts={alerts} />
            </div>
          )}

          {/* Main Content */}
          <main className="mx-auto max-w-[1600px] px-6 py-6 flex-1">
            {children}
          </main>
        </div>

        {/* Hosts Sidebar */}
        <HostsSidebar
          selectedHostId={selectedHostId}
          onHostSelect={setSelectedHostId}
        />
      </div>
    </HostProvider>
  );
}
