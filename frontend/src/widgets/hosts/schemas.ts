import { z } from 'zod';

// A fault the node-stats NODE running on a machine diagnosed about ITSELF —
// e.g. it is cut off from its Raft cluster because peers can't reach the
// address it advertises. It travels on the metric stream (the one channel an
// isolated node still has), lives in RAM on each receiver and disappears on
// its own once the node reports healthy again.
export const NodeAlertSchema = z.object({
  kind: z.string(), // 'raft_isolated'
  severity: z.string(), // 'warning' | 'error'
  title: z.string(),
  detail: z.string().optional().default(''),
  // One short sentence naming what to do — all a compact surface shows.
  action: z.string().optional().default(''),
  // What the operator can do, best option first (the full remedy).
  steps: z.array(z.string()).nullish().transform((v) => v ?? []),
  node_id: z.string().optional().default(''),
  advertise_addr: z.string().optional().default(''),
  advertise_url: z.string().optional().default(''),
  local_ipv4: z.string().optional().default(''),
  // Where this node's own dashboard answers now (the fault often makes its
  // advertised URL unusable), so the UI can link to its settings page.
  node_url: z.string().optional().default(''),
  since: z.string().optional().default(''),
});

export type NodeAlert = z.infer<typeof NodeAlertSchema>;

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
  hardware_uuid: z.string().optional().default(''),
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
  dashboard_url: z.string().optional().default(''),
  // Static hardware identity, served with the host row (from its latest metrics)
  // so the card renders these without waiting on the per-metric queries / SSE.
  cpu_model: z.string().optional().default(''),
  cpu_cores: z.number().optional().default(0),
  memory_total: z.number().optional().default(0), // bytes
  disk_total: z.number().optional().default(0), // bytes
  last_seen: z.string().optional().default(''),
  // Self-diagnosed fault of the node running on this machine (see above).
  node_alert: NodeAlertSchema.nullish().transform((v) => v ?? null),
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

// Connector-proposed identity update (rename / MAC change) on an existing
// host row, frozen for admin approval instead of being auto-applied.
export const PendingFieldChangeSchema = z.object({
  field: z.string(), // 'name' | 'mac_address'
  old: z.string().optional().default(''),
  new: z.string().optional().default(''),
});

export const HostPendingChangeSchema = z.object({
  change_id: z.string(),
  host_mac: z.string(),
  host_name: z.string().optional().default(''),
  source: z.string().optional().default(''), // 'proxmox' | 'pbs'
  changes: z.array(PendingFieldChangeSchema).nullish().transform((v) => v ?? []),
  status: z.string(), // 'pending' | 'rejected'
  created_at: z.string().optional().default(''),
  updated_at: z.string().optional().default(''),
});

export const PendingChangesResponseSchema = z.object({
  changes: z.array(HostPendingChangeSchema),
});

export type Host = z.infer<typeof HostSchema>;
export type HostsResponse = z.infer<typeof HostsResponseSchema>;
export type CurrentHostResponse = z.infer<typeof CurrentHostResponseSchema>;
export type HostHealth = z.infer<typeof HostHealthSchema>;
export type PendingFieldChange = z.infer<typeof PendingFieldChangeSchema>;
export type HostPendingChange = z.infer<typeof HostPendingChangeSchema>;
export type PendingChangesResponse = z.infer<typeof PendingChangesResponseSchema>;
