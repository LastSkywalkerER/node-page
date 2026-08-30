import { apiClient, unwrapEnvelope } from './api';

export interface User {
  id: number;
  email: string;
  role: string;
}

export interface AuthResponse {
  user: User;
  expires_in: number;
}

/**
 * The envelope can be present and still not carry a user (a proxy answering with
 * its own JSON, a half-written response). Catch that here rather than letting the
 * store blow up on `payload.user`.
 */
function assertAuthPayload(payload: AuthResponse | undefined, what: string): AuthResponse {
  if (!payload?.user) {
    throw new Error(`The server's ${what} response carried no user — please try again.`);
  }
  return payload;
}

class AuthService {
  async register(email: string, password: string, inviteToken?: string): Promise<AuthResponse> {
    const body: { email: string; password: string; invite_token?: string } = {
      email,
      password,
    };
    if (inviteToken) {
      body.invite_token = inviteToken;
    }
    const response = await apiClient.post('/auth/register', body);
    return assertAuthPayload(unwrapEnvelope<AuthResponse>(response, 'registration'), 'registration');
  }

  async login(email: string, password: string): Promise<AuthResponse> {
    const response = await apiClient.post('/auth/login', { email, password });
    return assertAuthPayload(unwrapEnvelope<AuthResponse>(response, 'sign-in'), 'sign-in');
  }

  async refresh(): Promise<number> {
    // Cookie is sent automatically; server sets new cookies in response
    const response = await apiClient.post('/auth/refresh');
    return unwrapEnvelope<{ expires_in: number }>(response, 'token refresh').expires_in;
  }

  async logout(): Promise<void> {
    try {
      await apiClient.post('/auth/logout');
    } catch (error) {
      console.warn('Server logout failed:', error);
    }
  }

  async getMe(): Promise<User> {
    const response = await apiClient.get('/users/me');
    return unwrapEnvelope<User>(response, 'the current user');
  }
}

export const authService = new AuthService();
