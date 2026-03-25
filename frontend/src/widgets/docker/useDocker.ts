import { createMetricHook } from '@/shared/hooks/useMetricQuery';
import type { DockerMetric } from './schemas';

export const useDocker = createMetricHook<DockerMetric, DockerMetric>('docker', 'docker-metrics');
