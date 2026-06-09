import { z } from 'zod';
import {
  dockerStatsSchema,
  dockerPortSchema,
  dockerMountSchema,
  dockerContainerSchema,
} from '@/widgets/docker/schemas';

// An application = one compose project / swarm stack / standalone container,
// aggregated across its containers. Mirrors the Go DockerApplication DTO.
export const dockerApplicationSchema = z.object({
  project: z.string(),
  display_name: z.string(),
  is_singleton: z.boolean().optional().default(false),
  total_containers: z.number(),
  running_containers: z.number(),
  service_count: z.number(),
  containers: z.array(dockerContainerSchema),
  stats: dockerStatsSchema,
  ports: z.array(dockerPortSchema),
  total_size_rw: z.number().optional().default(0),
  total_size_root_fs: z.number().optional().default(0),
  volumes: z.array(dockerMountSchema).optional().default([]),
  updates_available: z.number().optional().default(0),
  patches_available: z.number().optional().default(0),
  icon_slug: z.string().optional().default(''),
  public_url: z.string().optional().default(''),
  // host_id is attached by the all-hosts aggregation endpoint (Phase 2).
  host_id: z.number().optional(),
});

export const applicationsResponseSchema = z.object({
  applications: z.array(dockerApplicationSchema),
  docker_available: z.boolean(),
});

export const applicationDetailResponseSchema = z.object({
  application: dockerApplicationSchema,
});

export const composeViewSchema = z.object({
  project: z.string(),
  synthetic: z.string(),
  real: z.string().optional().default(''),
  real_available: z.boolean().optional().default(false),
  config_files: z.array(z.string()).optional().default([]),
});

export const applicationLogsResponseSchema = z.object({
  available: z.boolean(),
  logs: z.string(),
  reason: z.string().optional(),
  container: z.string().optional(),
});

export type DockerApplication = z.infer<typeof dockerApplicationSchema>;
export type ApplicationsResponse = z.infer<typeof applicationsResponseSchema>;
export type ComposeView = z.infer<typeof composeViewSchema>;
export type ApplicationLogsResponse = z.infer<typeof applicationLogsResponseSchema>;
