import { z } from 'zod';

// CPU metrics validation schemas
export const cpuMetricSchema = z.object({
  usage_percent: z.number(),
  cores: z.number(),
  load_avg_1: z.number(),
  load_avg_5: z.number(),
  load_avg_15: z.number(),
});

// Historical CPU data schema
export const historicalCPUSchema = z.object({
  timestamp: z.string(),
  usage: z.number(),
  load_avg_1: z.number(),
  load_avg_5: z.number(),
  load_avg_15: z.number(),
});

export type CPUMetric = z.infer<typeof cpuMetricSchema>;
export type HistoricalCPUMetric = z.infer<typeof historicalCPUSchema>;

// Legacy alias for backward compatibility
export type HistoricalCPU = HistoricalCPUMetric;
