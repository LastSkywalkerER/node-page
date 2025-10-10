import { useState } from 'react';
import { Menu, X } from 'lucide-react';
import { Button } from '@/shared/ui/button';
import { HostsWidget, ThemeSelectorWidget } from '@/widgets';

interface HostsSidebarProps {
  selectedHostId: number | null;
  onHostSelect: (hostId: number | null) => void;
}

export function HostsSidebar({ selectedHostId, onHostSelect }: HostsSidebarProps) {
  const [isOpen, setIsOpen] = useState(false);

  return (
    <>
      {/* Mobile Toggle Button */}
      <div className="lg:hidden fixed top-4 right-4 z-50">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setIsOpen(!isOpen)}
          className="bg-black/20 border-white/20 backdrop-blur-sm"
        >
          {isOpen ? <X className="w-4 h-4" /> : <Menu className="w-4 h-4" />}
        </Button>
      </div>

      {/* Overlay for mobile */}
      {isOpen && (
        <div
          className="lg:hidden fixed inset-0 bg-black/50 z-40"
          onClick={() => setIsOpen(false)}
        />
      )}

      {/* Sidebar */}
      <div className={`
        fixed lg:static top-0 right-0 h-full w-80 lg:w-64
        bg-black/20 backdrop-blur-xl border-l border-white/10
        transform transition-transform duration-300 ease-in-out z-50
        ${isOpen ? 'translate-x-0' : 'translate-x-full lg:translate-x-0'}
      `}>
        <div className="p-4 h-full overflow-y-auto space-y-4">
          <ThemeSelectorWidget />
          <HostsWidget
            selectedHostId={selectedHostId}
            onHostSelect={(hostId) => {
              onHostSelect(hostId);
              setIsOpen(false); // Close sidebar on mobile after selection
            }}
          />
        </div>
      </div>
    </>
  );
}
