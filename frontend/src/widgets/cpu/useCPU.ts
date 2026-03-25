import { createMetricHook } from '@/shared/hooks/useMetricQuery';
import type { CPUMetric, HistoricalCPUMetric } from './schemas';

export const useCPU = createMetricHook<CPUMetric, HistoricalCPUMetric>('cpu', 'cpu-metrics');
