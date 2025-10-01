import { z } from 'zod';

// Connection status validation schemas
export const connectionStatusSchema = z.object({
  isConnected: z.boolean(),
  latency: z.number().nullable(),
});

export const healthResponseSchema = z.object({
  status: z.string(),
});

export type ConnectionStatus = z.infer<typeof connectionStatusSchema>;
export type HealthResponse = z.infer<typeof healthResponseSchema>;
