import { createMetricHook } from '@/shared/hooks/useMetricQuery';
import type { DiskMetric, HistoricalDiskMetric } from './schemas';

export const useDisk = createMetricHook<DiskMetric, HistoricalDiskMetric>('disk', 'disk-metrics');
