import { apiClient } from './api';

export interface User {
  id: number;
  email: string;
  role: string;
}

export interface AuthResponse {
  user: User;
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

interface ApiEnvelope<T> {
  data: T;
}

export interface RefreshResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

class AuthService {
  async register(email: string, password: string): Promise<AuthResponse> {
    const response = await apiClient.post<ApiEnvelope<AuthResponse>>('/auth/register', {
      email,
      password,
    });

    const payload = response.data.data;

    return payload;
  }

  async login(email: string, password: string): Promise<AuthResponse> {
    const response = await apiClient.post<ApiEnvelope<AuthResponse>>('/auth/login', {
      email,
      password,
    });

    const payload = response.data.data;

    return payload;
  }

  async refresh(refreshToken: string): Promise<RefreshResponse> {
    const response = await apiClient.post<ApiEnvelope<RefreshResponse>>('/auth/refresh', {
      refresh_token: refreshToken,
    });

    const payload = response.data.data;

    return payload;
  }

  async logout(refreshToken?: string): Promise<void> {
    try {
      if (refreshToken) {
        await apiClient.post('/auth/logout', { refresh_token: refreshToken });
      }
    } catch (error) {
      console.warn('Server logout failed:', error);
    }
  }

  async getMe(): Promise<User> {
    const response = await apiClient.get<{ data: User }>('/users/me');
    return response.data.data;
  }
}

export const authService = new AuthService();

