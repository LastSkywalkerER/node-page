// Validation schemas for auth widgets
import * as yup from 'yup';

export const loginSchema = yup.object({
  email: yup.string().email('Invalid email address').required('Email is required'),
  password: yup.string().min(8, 'Password must be at least 8 characters').required('Password is required'),
});

export const registerSchema = yup.object({
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

export type LoginFormData = yup.InferType<typeof loginSchema>;

export type RegisterFormData = yup.InferType<typeof registerSchema>;

export interface AuthResponse {
  user: {
    id: number;
    email: string;
    role: string;
  };
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

