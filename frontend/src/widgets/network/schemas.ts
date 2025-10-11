import { z } from 'zod';

// Network metrics validation schemas
export const networkInterfaceSchema = z.object({
  name: z.string(),
  ips: z.array(z.string()).optional().default([]),
  mac: z.string().optional().default(''),
  bytes_sent: z.number(),
  bytes_recv: z.number(),
  packets_sent: z.number(),
  packets_recv: z.number(),
  speed_kbps_sent: z.number(),
  speed_kbps_recv: z.number(),
  is_primary: z.boolean().optional().default(false),
  errin: z.number().optional().default(0),
  errout: z.number().optional().default(0),
  dropin: z.number().optional().default(0),
  dropout: z.number().optional().default(0),
});

export const networkMetricSchema = z.object({
  interfaces: z.array(networkInterfaceSchema),
});

// Historical network data schema - same structure as current metrics but with timestamp
export const historicalNetworkSchema = z.object({
  interfaces: z.array(networkInterfaceSchema),
});

export type NetworkInterface = z.infer<typeof networkInterfaceSchema>;
export type NetworkMetric = z.infer<typeof networkMetricSchema>;
export type HistoricalNetworkMetric = z.infer<typeof historicalNetworkSchema>;

// Legacy alias for backward compatibility
export type HistoricalNetwork = HistoricalNetworkMetric;
