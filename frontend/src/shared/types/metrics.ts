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

