import { ReactNode, useState, useEffect } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useAlerts } from '@/shared/lib/store';
import { HostProvider } from '@/shared/lib/HostContext';
import ConnectionStatusWidget from '@/widgets/connection-status/ConnectionStatusWidget';
import AlertsPanel from './components/AlertsPanel';
import { HostsSidebar } from './components/HostsSidebar';
import { useHosts } from '@/widgets/hosts/useHosts';

interface DashboardProps {
  children: ReactNode;
}

export default function Dashboard({ children }: DashboardProps) {
  const alerts = useAlerts();
  const [searchParams, setSearchParams] = useSearchParams();
  const initialHostIdParam = searchParams.get('host_id');
  const initialHostId = initialHostIdParam ? Number(initialHostIdParam) : null;
  const [selectedHostId, setSelectedHostId] = useState<number | null>(initialHostId);
  const { data: hostsData } = useHosts();

  // Sync selectedHostId with URL query param `host_id`
  useEffect(() => {
    const hostIdParam = searchParams.get('host_id');
    const parsed = hostIdParam ? Number(hostIdParam) : null;
    if ((parsed || null) !== selectedHostId) {
      setSelectedHostId(parsed);
    }
  }, [searchParams]);

  // If no host_id provided, default to the first host from the list
  useEffect(() => {
    if (selectedHostId === null) {
      const currentParam = searchParams.get('host_id');
      const firstHostId = hostsData?.hosts?.[0]?.id;
      if (!currentParam && firstHostId !== undefined) {
        setSelectedHostId(firstHostId);
      }
    }
  }, [hostsData, selectedHostId, searchParams]);

  // When selection changes, update URL param
  useEffect(() => {
    const currentParam = searchParams.get('host_id');
    const nextParam = selectedHostId !== null ? String(selectedHostId) : null;
    if (currentParam !== nextParam) {
      const newParams = new URLSearchParams(searchParams);
      if (nextParam === null) {
        newParams.delete('host_id');
      } else {
        newParams.set('host_id', nextParam);
      }
      setSearchParams(newParams, { replace: true });
    }
  }, [selectedHostId]);

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
