// Validation schemas for auth widgets
// Note: Validation is handled in the widgets using react-hook-form and yup
// This file can be used for additional schema definitions if needed

export interface LoginFormData {
  email: string;
  password: string;
}

export interface RegisterFormData {
  email: string;
  password: string;
  confirmPassword: string;
}

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

