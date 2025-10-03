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
});

export const dockerMetricSchema = z.object({
  containers: z.array(dockerContainerSchema),
  total_containers: z.number(),
  running_containers: z.number(),
  docker_available: z.boolean(),
  error: z.string().optional(),
});

export type DockerStats = z.infer<typeof dockerStatsSchema>;
export type DockerPort = z.infer<typeof dockerPortSchema>;
export type DockerContainer = z.infer<typeof dockerContainerSchema>;
export type DockerMetric = z.infer<typeof dockerMetricSchema>;
