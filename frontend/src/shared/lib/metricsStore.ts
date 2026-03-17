import { create } from 'zustand';

// Minimal metric type shapes matching backend JSON output
interface CPUMetric {
  usage_percent: number;
  cores: number;
  load_avg_1: number;
  load_avg_5: number;
  load_avg_15: number;
}

interface MemoryMetric {
  usage_percent: number;
  total: number;
  used: number;
  available: number;
}

interface DiskMetric {
  usage_percent: number;
  total: number;
  used: number;
  free: number;
}

interface NetworkInterface {
  name: string;
  bytes_sent: number;
  bytes_recv: number;
}

interface NetworkMetric {
  interfaces: NetworkInterface[];
}

interface DockerMetric {
  total_containers: number;
  running_containers: number;
  docker_available: boolean;
}

interface MetricsPayload {
  cpu?: CPUMetric;
  memory?: MemoryMetric;
  disk?: DiskMetric;
  network?: NetworkMetric;
  docker?: DockerMetric;
}

interface MetricsState extends MetricsPayload {
  setMetrics: (data: MetricsPayload) => void;
}

export const useMetricsStore = create<MetricsState>((set) => ({
  cpu: undefined,
  memory: undefined,
  disk: undefined,
  network: undefined,
  docker: undefined,
  setMetrics: (data) => set((state) => ({ ...state, ...data })),
}));
