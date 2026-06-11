import { z } from 'zod';

export const HostSchema = z.object({
  id: z.number(),
  name: z.string(),
  display_name: z.string().optional().default(''),
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
  // Virtualization topology (filled by connectors, e.g. Proxmox):
  // '' | 'hypervisor' | 'vm' | 'lxc'
  host_type: z.string().optional().default(''),
  // Local row id of the hypervisor this guest runs on; 0/absent = top-level.
  parent_id: z.number().optional().default(0),
  parent_mac: z.string().optional().default(''),
  // '' / 'agent' (legacy default) | 'connector' | 'agent+connector'
  source: z.string().optional().default(''),
  external_id: z.string().optional().default(''),
  // Hypervisor-reported power state for connector guests: running | stopped | paused | online | offline
  guest_status: z.string().optional().default(''),
  // Uplink site this row arrived from over the cross-cluster bridge ('' = local cluster).
  origin_cluster: z.string().optional().default(''),
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
