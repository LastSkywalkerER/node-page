import axios, { AxiosInstance, InternalAxiosRequestConfig, AxiosError, AxiosResponse } from 'axios';
import { useUserStore } from '../store/user';

const apiClient: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  withCredentials: true, // send HttpOnly cookies on every request
  headers: { 'Content-Type': 'application/json' },
  timeout: 15000,
});

let refreshPromise: Promise<number> | null = null;

/**
 * Single-flight access-token refresh. BOTH the response interceptor (reactive,
 * on a 401) and the proactive timer in the user store call this, so the two
 * paths can never fire two concurrent `/auth/refresh` requests that race into a
 * refresh-token rotation conflict — the loser of which gets
 * `invalid_or_revoked_refresh` and would log the user out despite a successful
 * refresh. Concurrent callers share the in-flight promise; on success the next
 * proactive refresh is scheduled. Resolves to the new access-token TTL (seconds).
 */
export function refreshSession(): Promise<number> {
  if (!refreshPromise) {
    refreshPromise = apiClient
      .post<{ data: { expires_in: number } }>('/auth/refresh')
      .then((res: AxiosResponse<{ data: { expires_in: number } }>) => {
        const expiresIn = res.data?.data?.expires_in ?? 0;
        if (expiresIn > 0) {
          useUserStore.getState().scheduleTokenRefresh(expiresIn);
        }
        return expiresIn;
      })
      .finally(() => {
        refreshPromise = null;
      });
  }
  return refreshPromise;
}

// Response interceptor: on 401 attempt a silent token refresh via cookie, then retry once
apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean };

    // Don't retry for auth endpoints to avoid infinite loops
    if (
      originalRequest?.url?.includes('/auth/refresh') ||
      originalRequest?.url?.includes('/auth/login') ||
      originalRequest?.url?.includes('/auth/register')
    ) {
      return Promise.reject(error);
    }

    if (error.response?.status === 401 && !originalRequest._retry) {
      originalRequest._retry = true;
      try {
        await refreshSession();
        return apiClient(originalRequest);
      } catch {
        useUserStore.getState().clearAuth();
        return Promise.reject(error);
      }
    }

    return Promise.reject(error);
  }
);

export { apiClient };
