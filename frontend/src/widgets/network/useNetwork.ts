import { createMetricHook } from '@/shared/hooks/useMetricQuery';
import type { HistoricalNetworkMetric, NetworkMetric } from './schemas';

export const useNetwork = createMetricHook<NetworkMetric, HistoricalNetworkMetric & { timestamp: string }>('network', 'network-metrics');
