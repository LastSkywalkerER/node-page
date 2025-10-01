import { create } from 'zustand';
import { subscribeWithSelector } from 'zustand/middleware';
import type { TimeRange, DashboardFilters, Alert } from '@/shared/types/metrics';

interface DashboardState {
  // UI State
  isConnected: boolean;
  latency: number | null;
  alerts: Alert[];
  selectedContainer: string | null;
  filters: DashboardFilters;

  // Actions
  setConnected: (connected: boolean) => void;
  setLatency: (latency: number | null) => void;
  addAlert: (alert: Alert) => void;
  removeAlert: (id: string) => void;
  clearAlerts: () => void;
  setSelectedContainer: (containerId: string | null) => void;
  setFilters: (filters: Partial<DashboardFilters>) => void;
}

export const useDashboardStore = create<DashboardState>()(
  subscribeWithSelector((set) => ({
    // Initial state
    isConnected: false,
    latency: null,
    alerts: [],
    selectedContainer: null,
    filters: {
      timeRange: '5m',
      showSystem: true,
      showDocker: true,
      showNetwork: true,
    },

    // Actions
    setConnected: (isConnected) => set({ isConnected }),

    setLatency: (latency) => set({ latency }),

    addAlert: (alert) =>
      set((state) => ({
        alerts: [...state.alerts.slice(-9), alert], // Keep last 10 alerts
      })),

    removeAlert: (id) =>
      set((state) => ({
        alerts: state.alerts.filter((alert) => alert.id !== id),
      })),

    clearAlerts: () => set({ alerts: [] }),

    setSelectedContainer: (selectedContainer) => set({ selectedContainer }),

    setFilters: (newFilters) =>
      set((state) => ({
        filters: { ...state.filters, ...newFilters },
      })),
  }))
);

// Selectors
export const useConnectionStatus = () =>
  useDashboardStore((state) => ({
    isConnected: state.isConnected,
    latency: state.latency,
  }));
export const useAlerts = () => useDashboardStore((state) => state.alerts);
export const useSelectedContainer = () =>
  useDashboardStore((state) => state.selectedContainer);
export const useFilters = () => useDashboardStore((state) => state.filters);
