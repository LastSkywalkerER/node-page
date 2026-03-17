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
  };
}

export interface CompleteSetupResponse {
  message: string;
}
