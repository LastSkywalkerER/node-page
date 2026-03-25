import { createMetricHook } from '@/shared/hooks/useMetricQuery';
import type { MemoryMetric, HistoricalMemoryMetric } from './schemas';

export const useMemory = createMetricHook<MemoryMetric, HistoricalMemoryMetric>('memory', 'memory-metrics');
