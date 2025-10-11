import { z } from 'zod';

// Memory metrics validation schemas
export const memoryMetricSchema = z.object({
  total: z.number(),
  available: z.number(),
  used: z.number(),
  usage_percent: z.number(),
  free: z.number(),
  cached: z.number().optional(),
  buffers: z.number().optional(),
  swap_total: z.number().optional(),
  swap_used: z.number().optional(),
  active: z.number().optional(),
  inactive: z.number().optional(),
  shared: z.number().optional(),
  swap_free: z.number().optional(),
});

// Historical memory data schema
export const historicalMemorySchema = z.object({
  timestamp: z.string(),
  usage_percent: z.number(),
  used_bytes: z.number(),
  total_bytes: z.number(),
});

export type MemoryMetric = z.infer<typeof memoryMetricSchema>;
export type HistoricalMemoryMetric = z.infer<typeof historicalMemorySchema>;

// Legacy alias for backward compatibility
export type HistoricalMemory = HistoricalMemoryMetric;
