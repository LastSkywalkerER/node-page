import { z } from 'zod';

// Disk metrics validation schemas
export const diskMetricSchema = z.object({
  total: z.number(),
  used: z.number(),
  free: z.number(),
  usage_percent: z.number(),
});

// Historical disk data schema
export const historicalDiskSchema = z.object({
  timestamp: z.string(),
  usage_percent: z.number(),
  used_bytes: z.number(),
  total_bytes: z.number(),
});

export type DiskMetric = z.infer<typeof diskMetricSchema>;
export type HistoricalDiskMetric = z.infer<typeof historicalDiskSchema>;

// Legacy alias for backward compatibility
export type HistoricalDisk = HistoricalDiskMetric;
