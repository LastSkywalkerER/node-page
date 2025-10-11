import { z } from 'zod';

export const HostSchema = z.object({
  id: z.number(),
  name: z.string(),
  mac_address: z.string(),
  ipv4: z.string().optional().default(''),
  os: z.string().optional().default(''),
  platform: z.string().optional().default(''),
  platform_family: z.string().optional().default(''),
  platform_version: z.string().optional().default(''),
  kernel_version: z.string().optional().default(''),
  virtualization_system: z.string().optional().default(''),
  virtualization_role: z.string().optional().default(''),
  system_host_id: z.string().optional().default(''),
  last_seen: z.string().optional().default(''),
  created_at: z.string(),
  updated_at: z.string(),
});

export const HostsResponseSchema = z.object({
  hosts: z.array(HostSchema),
});

export const CurrentHostResponseSchema = z.object({
  host: HostSchema,
});

export const HostHealthSchema = z.object({
  host_id: z.number(),
  status: z.string(),
  latency_ms: z.number(),
  uptime_seconds: z.number(),
  last_seen: z.string(),
});

export type Host = z.infer<typeof HostSchema>;
export type HostsResponse = z.infer<typeof HostsResponseSchema>;
export type CurrentHostResponse = z.infer<typeof CurrentHostResponseSchema>;
export type HostHealth = z.infer<typeof HostHealthSchema>;
