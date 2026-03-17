import { apiClient } from './api';

export interface User {
  id: number;
  email: string;
  role: string;
}

export interface AuthResponse {
  user: User;
  expires_in: number;
}

interface ApiEnvelope<T> {
  data: T;
}

class AuthService {
  async register(email: string, password: string): Promise<AuthResponse> {
    const response = await apiClient.post<ApiEnvelope<AuthResponse>>('/auth/register', {
      email,
      password,
    });
    return response.data.data;
  }

  async login(email: string, password: string): Promise<AuthResponse> {
    const response = await apiClient.post<ApiEnvelope<AuthResponse>>('/auth/login', {
      email,
      password,
    });
    return response.data.data;
  }

  async refresh(): Promise<void> {
    // Cookie is sent automatically; server sets new cookies in response
    await apiClient.post('/auth/refresh');
  }

  async logout(): Promise<void> {
    try {
      await apiClient.post('/auth/logout');
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
