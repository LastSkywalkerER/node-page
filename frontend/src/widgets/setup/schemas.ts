// Validation schemas for setup wizard
import * as yup from 'yup';

export const setupConfigSchema = yup.object({
  jwt_secret: yup.string().min(16, 'JWT secret must be at least 16 characters').required('JWT secret is required'),
  refresh_secret: yup.string().min(16, 'Refresh secret must be at least 16 characters').required('Refresh secret is required'),
  addr: yup.string().default(':8080'),
  gin_mode: yup.string().oneOf(['debug', 'release'], 'Gin mode must be debug or release').default('release'),
  debug: yup.string().oneOf(['true', 'false'], 'Debug must be true or false').default('false'),
  db_type: yup.string().default('sqlite'),
  db_dsn: yup.string().default('stats.db'),
});

export const adminUserSchema = yup.object({
  email: yup.string().email('Invalid email address').required('Email is required'),
  password: yup
    .string()
    .min(8, 'Password must be at least 8 characters')
    .matches(
      /^(?=.*[a-zA-Z])(?=.*\d)/,
      'Password must contain at least one letter and one number'
    )
    .required('Password is required'),
  confirmPassword: yup
    .string()
    .oneOf([yup.ref('password')], 'Passwords must match')
    .required('Please confirm your password'),
});

export const completeSetupSchema = yup.object({
  config: setupConfigSchema.required(),
  admin_email: yup.string().email('Invalid email address').required('Email is required'),
  admin_password: yup
    .string()
    .min(8, 'Password must be at least 8 characters')
    .matches(
      /^(?=.*[a-zA-Z])(?=.*\d)/,
      'Password must contain at least one letter and one number'
    )
    .required('Password is required'),
});

export type SetupConfigFormData = yup.InferType<typeof setupConfigSchema>;
export type AdminUserFormData = yup.InferType<typeof adminUserSchema>;
export type CompleteSetupFormData = yup.InferType<typeof completeSetupSchema>;

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

