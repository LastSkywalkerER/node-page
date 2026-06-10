import { z } from 'zod';

// Docker metrics validation schemas
export const dockerStatsSchema = z.object({
  cpu_percent: z.number(),
  memory_usage: z.number(),
  memory_limit: z.number(),
  memory_percent: z.number(),
  network_rx: z.number(),
  network_tx: z.number(),
  block_read: z.number(),
  block_write: z.number(),
});

export const dockerPortSchema = z.object({
  private_port: z.number(),
  public_port: z.number().optional(),
  type: z.string(),
  ip: z.string().optional(),
  // External URL a reverse proxy (Traefik) routes to this port, if detected.
  public_url: z.string().optional(),
});

export const dockerMountSchema = z.object({
  type: z.string(),
  name: z.string().optional(),
  source: z.string(),
  destination: z.string(),
  rw: z.boolean(),
  // Named-volume on-disk size in bytes (0/absent for bind/tmpfs or unknown).
  size: z.number().optional(),
});

export const dockerContainerSchema = z.object({
  id: z.string(),
  name: z.string(),
  image: z.string(),
  state: z.string(),
  status: z.string(),
  ports: z.array(dockerPortSchema),
  stats: dockerStatsSchema,
  created: z.string(),
  finished_at: z.string().optional(),
  // Application grouping metadata (read-only label discovery)
  project: z.string().optional(),
  service: z.string().optional(),
  labels: z.record(z.string(), z.string()).optional(),
  compose_config_files: z.string().optional(),
  compose_working_dir: z.string().optional(),
  // Disk footprint + mounts
  size_rw: z.number().optional(),
  size_root_fs: z.number().optional(),
  mounts: z.array(dockerMountSchema).optional(),
  // Image identity + update check
  image_id: z.string().optional(),
  update_available: z.boolean().optional(),
  update_checked: z.boolean().optional(),
  local_digest: z.string().optional(),
  remote_digest: z.string().optional(),
  image_version: z.string().optional(),
  remote_version: z.string().optional(),
  // Newer registry version tags (independent of the same-tag digest check).
  newer_version: z.string().optional(), // newest same-major tag (e.g. 1.2.1 → 1.3.2)
  newer_major_version: z.string().optional(), // newest higher-major tag (e.g. 1.2.1 → 2.0.0)
});

export const dockerStackSchema = z.object({
  name: z.string(),
  containers: z.array(dockerContainerSchema),
  total_containers: z.number(),
  running_containers: z.number(),
});

export const dockerMetricSchema = z.object({
  stacks: z.array(dockerStackSchema),
  total_containers: z.number(),
  running_containers: z.number(),
  docker_available: z.boolean(),
  error: z.string().optional(),
});

export type DockerStats = z.infer<typeof dockerStatsSchema>;
export type DockerPort = z.infer<typeof dockerPortSchema>;
export type DockerMount = z.infer<typeof dockerMountSchema>;
export type DockerContainer = z.infer<typeof dockerContainerSchema>;
export type DockerStack = z.infer<typeof dockerStackSchema>;
export type DockerMetric = z.infer<typeof dockerMetricSchema>;
