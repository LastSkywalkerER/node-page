import { z } from 'zod';

// Import widget schemas
import { cpuMetricSchema } from '@/widgets/cpu/schemas';
import { memoryMetricSchema } from '@/widgets/memory/schemas';
import { diskMetricSchema } from '@/widgets/disk/schemas';
import { networkMetricSchema } from '@/widgets/network/schemas';
import { dockerMetricSchema } from '@/widgets/docker/schemas';
import { historicalCPUSchema } from '@/widgets/cpu/schemas';
import { historicalMemorySchema } from '@/widgets/memory/schemas';
import { historicalDiskSchema } from '@/widgets/disk/schemas';
import { historicalNetworkSchema } from '@/widgets/network/schemas';

// System metrics schema combining all widget schemas
export const systemMetricSchema = z.object({
  timestamp: z.string(),
  cpu: cpuMetricSchema,
  memory: memoryMetricSchema,
  disk: diskMetricSchema,
  network: networkMetricSchema,
  docker: dockerMetricSchema,
});

// Historical data schema combining all historical widget schemas
export const historicalDataSchema = z.object({
  cpu: z.array(historicalCPUSchema),
  memory: z.array(historicalMemorySchema),
  disk: z.array(historicalDiskSchema),
  network: z.array(historicalNetworkSchema),
});

// WebSocket message schemas
export const wsMessageSchema = z.object({
  type: z.enum(['metrics', 'alert', 'error']),
  data: z.union([systemMetricSchema, z.any(), z.string()]),
  timestamp: z.string(),
});

export type SystemMetric = z.infer<typeof systemMetricSchema>;
export type HistoricalData = z.infer<typeof historicalDataSchema>;
export type WSMessage = z.infer<typeof wsMessageSchema>;
