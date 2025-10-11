import { z } from 'zod';

// Disk metrics validation schemas
export const diskMetricSchema = z.object({
  total: z.number(),
  used: z.number(),
  free: z.number(),
  usage_percent: z.number(),
  partitions: z.array(z.object({
    device: z.string(),
    mountpoint: z.string(),
    fstype: z.string(),
    opts: z.string(),
  })).optional().default([]),
  mounts: z.array(z.object({
    path: z.string(),
    fstype: z.string(),
    total: z.number(),
    free: z.number(),
    used: z.number(),
    used_percent: z.number(),
    inodes_total: z.number(),
    inodes_used: z.number(),
    inodes_free: z.number(),
    inodes_used_percent: z.number(),
  })).optional().default([]),
  io_counters: z.array(z.object({
    name: z.string(),
    read_count: z.number(),
    merged_read_count: z.number(),
    write_count: z.number(),
    merged_write_count: z.number(),
    read_bytes: z.number(),
    write_bytes: z.number(),
    read_time: z.number(),
    write_time: z.number(),
    iops_in_progress: z.number(),
    io_time: z.number(),
    weighted_io: z.number(),
    serial_number: z.string().optional().default(''),
    label: z.string().optional().default(''),
  })).optional().default([]),
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
