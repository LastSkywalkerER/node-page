import { z } from 'zod';

export const DiscoveredHintSchema = z.object({
  type: z.string(),
  confidence: z.string(),
  guest_kind: z.string(),
  vmid_hint: z.number().optional().default(0),
  suggested_endpoint: z.string().optional().default(''),
  evidence: z.array(z.string()).optional().default([]),
});

export const ConnectorSchema = z.object({
  id: z.number(),
  type: z.string(),
  endpoint: z.string().optional().default(''),
  token_id: z.string().optional().default(''),
  skip_tls_verify: z.boolean().optional().default(false),
  fingerprint: z.string(),
  // Type-specific JSON settings (Pexels: {"query":"..."}).
  config: z.string().optional().default(''),
  enabled: z.boolean().optional().default(false),
  status: z.string().optional().default(''),
  last_sync_at: z.string().optional().default(''),
  last_error: z.string().optional().default(''),
  created_at: z.string().optional().default(''),
  updated_at: z.string().optional().default(''),
});

export const ConnectorsListSchema = z.object({
  discovered: z.array(DiscoveredHintSchema).optional().default([]),
  configured: z.array(ConnectorSchema).optional().default([]),
});

export const ConnectorPreviewSchema = z.object({
  fingerprint: z.string(),
  cluster_name: z.string().optional().default(''),
  version: z.string().optional().default(''),
  nodes: z.array(z.string()).optional().default([]),
  guest_count: z.number().optional().default(0),
  matched_hosts: z.number().optional().default(0),
});

export type DiscoveredHint = z.infer<typeof DiscoveredHintSchema>;
export type Connector = z.infer<typeof ConnectorSchema>;
export type ConnectorsList = z.infer<typeof ConnectorsListSchema>;
export type ConnectorPreview = z.infer<typeof ConnectorPreviewSchema>;

export interface ConnectRequest {
  endpoint: string;
  token_id: string;
  secret: string;
  skip_tls_verify: boolean;
}
