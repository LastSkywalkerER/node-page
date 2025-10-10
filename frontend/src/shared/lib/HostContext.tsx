import { createContext, useContext, ReactNode } from 'react';

interface HostContextType {
  selectedHostId: number | null;
  setSelectedHostId: (hostId: number | null) => void;
}

const HostContext = createContext<HostContextType | undefined>(undefined);

interface HostProviderProps {
  children: ReactNode;
  selectedHostId: number | null;
  setSelectedHostId: (hostId: number | null) => void;
}

export function HostProvider({ children, selectedHostId, setSelectedHostId }: HostProviderProps) {
  return (
    <HostContext.Provider value={{ selectedHostId, setSelectedHostId }}>
      {children}
    </HostContext.Provider>
  );
}

export function useHost() {
  const context = useContext(HostContext);
  if (context === undefined) {
    throw new Error('useHost must be used within a HostProvider');
  }
  return context;
}
