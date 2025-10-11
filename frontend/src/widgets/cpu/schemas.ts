import { z } from 'zod';

// CPU metrics validation schemas
export const cpuMetricSchema = z.object({
  usage_percent: z.number(),
  cores: z.number(),
  load_avg_1: z.number(),
  load_avg_5: z.number(),
  load_avg_15: z.number(),
  temperature: z.number(),
  // CPU info fields
  vendor_id: z.string().optional().default(''),
  family: z.string().optional().default(''),
  model: z.string().optional().default(''),
  model_name: z.string().optional().default(''),
  mhz: z.number().optional().default(0),
  cache_size: z.number().optional().default(0),
  flags: z.array(z.string()).optional().default([]),
  microcode: z.string().optional().default(''),
  // CPU times
  user: z.number().optional().default(0),
  system: z.number().optional().default(0),
  idle: z.number().optional().default(0),
  nice: z.number().optional().default(0),
  iowait: z.number().optional().default(0),
  irq: z.number().optional().default(0),
  softirq: z.number().optional().default(0),
  steal: z.number().optional().default(0),
  guest: z.number().optional().default(0),
  guest_nice: z.number().optional().default(0),
});

// Historical CPU data schema
export const historicalCPUSchema = z.object({
  timestamp: z.string(),
  usage: z.number(),
  load_avg_1: z.number(),
  load_avg_5: z.number(),
  load_avg_15: z.number(),
  temperature: z.number(),
});

export type CPUMetric = z.infer<typeof cpuMetricSchema>;
export type HistoricalCPUMetric = z.infer<typeof historicalCPUSchema>;

// Legacy alias for backward compatibility
export type HistoricalCPU = HistoricalCPUMetric;
