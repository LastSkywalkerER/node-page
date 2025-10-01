// Import widget types
import type { CPUMetric } from '@/widgets/cpu/schemas';
import type { MemoryMetric } from '@/widgets/memory/schemas';
import type { DiskMetric } from '@/widgets/disk/schemas';
import type { NetworkMetric } from '@/widgets/network/schemas';
import type { DockerMetric } from '@/widgets/docker/schemas';
import type { HistoricalCPUMetric } from '@/widgets/cpu/schemas';
import type { HistoricalMemoryMetric } from '@/widgets/memory/schemas';
import type { HistoricalDiskMetric } from '@/widgets/disk/schemas';
import type { HistoricalNetworkMetric } from '@/widgets/network/schemas';

export interface SystemMetric {
  timestamp: string;
  cpu: CPUMetric;
  memory: MemoryMetric;
  disk: DiskMetric;
  network: NetworkMetric;
  docker: DockerMetric;
}

// Historical data
export interface HistoricalData {
  cpu: HistoricalCPUMetric[];
  memory: HistoricalMemoryMetric[];
  disk: HistoricalDiskMetric[];
  network: HistoricalNetworkMetric[];
}

// UI State types
export type ThemeType = 'glass-aurora' | 'neon-terminal' | 'slate-pro' | 'cards-flow';

export type TimeRange = '5m' | '1h' | '24h' | '7d';

export interface DashboardFilters {
  timeRange: TimeRange;
  host?: string;
  showSystem: boolean;
  showDocker: boolean;
  showNetwork: boolean;
}

export interface Alert {
  id: string;
  level: 'info' | 'warn' | 'crit';
  message: string;
  timestamp: string;
  metric?: string;
  value?: number;
}
