import { ReactNode } from 'react';
import { useAlerts } from '@/shared/lib/store';
import ThemeSelector from './components/ThemeSelector';
import ConnectionStatusWidget from '@/widgets/connection-status/ConnectionStatusWidget';
import AlertsPanel from './components/AlertsPanel';

interface DashboardProps {
  children: ReactNode;
}

export default function Dashboard({ children }: DashboardProps) {
  const alerts = useAlerts();

  return (
    <div className="min-h-screen">
      {/* Header */}
      <header className="sticky top-0 z-50 border-b border-white/10 bg-black/20 backdrop-blur-xl">
        <div className="mx-auto max-w-[1600px] px-6 py-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-6">
              <ConnectionStatusWidget />
            </div>

            <div className="flex items-center space-x-4">
              <ThemeSelector />
            </div>
          </div>

        </div>
      </header>

      {/* Alerts */}
      {alerts.length > 0 && (
        <div className="mx-auto max-w-[1600px] px-6 py-4">
          <AlertsPanel alerts={alerts} />
        </div>
      )}

      {/* Main Content */}
      <main className="mx-auto max-w-[1600px] px-6 py-6">
        {children}
      </main>
    </div>
  );
}
