// Validation schemas for setup wizard
import { z } from 'zod';

export const setupConfigSchema = z.object({
  jwt_secret: z.string().min(16, 'JWT secret must be at least 16 characters'),
  refresh_secret: z.string().min(16, 'Refresh secret must be at least 16 characters'),
  addr: z.string(),
  gin_mode: z.enum(['debug', 'release'], { message: 'Gin mode must be debug or release' }),
  debug: z.enum(['true', 'false'], { message: 'Debug must be true or false' }),
  db_type: z.string(),
  db_dsn: z.string(),
  prometheus_enabled: z.enum(['true', 'false']),
  prometheus_auth: z.enum(['true', 'false']),
  prometheus_token: z.string(),
  docker_host_metrics_compat: z.boolean(),
});

const passwordSchema = z
  .string()
  .min(8, 'Password must be at least 8 characters')
  .regex(/^(?=.*[a-zA-Z])(?=.*\d)/, 'Password must contain at least one letter and one number');

export const adminUserSchema = z.object({
  email: z.string().email('Invalid email address'),
  password: passwordSchema,
  confirmPassword: z.string(),
}).refine((data) => data.password === data.confirmPassword, {
  message: 'Passwords must match',
  path: ['confirmPassword'],
});

export const completeSetupSchema = z.object({
  config: setupConfigSchema,
  admin_email: z.string().email('Invalid email address'),
  admin_password: passwordSchema,
});

export type SetupConfigFormData = z.infer<typeof setupConfigSchema>;
export type AdminUserFormData = z.infer<typeof adminUserSchema>;
export type CompleteSetupFormData = z.infer<typeof completeSetupSchema>;

export interface SetupStatusResponse {
  setup_needed: boolean;
  running_in_docker?: boolean;
}

export interface ConfigResponse {
  config: {
    jwt_secret: string;
    refresh_secret: string;
    addr: string;
    gin_mode: string;
    debug: string;
    db_type: string;
    db_dsn: string;
    prometheus_enabled: string;
    prometheus_auth: string;
    prometheus_token: string;
  };
}

export interface CompleteSetupResponse {
  message: string;
}

/** API body.config shape (snake_case) for setup preview and complete. */
export function toSetupConfigApiPayload(config: SetupConfigFormData) {
  return {
    jwt_secret: config.jwt_secret,
    refresh_secret: config.refresh_secret,
    addr: config.addr || ':8080',
    gin_mode: config.gin_mode || 'release',
    debug: config.debug || 'false',
    db_type: config.db_type || 'sqlite',
    db_dsn: config.db_dsn || 'stats.db',
    prometheus_enabled: config.prometheus_enabled || 'false',
    prometheus_auth: config.prometheus_auth || 'false',
    prometheus_token: config.prometheus_token || '',
    docker_host_metrics_compat: config.docker_host_metrics_compat,
  };
}
