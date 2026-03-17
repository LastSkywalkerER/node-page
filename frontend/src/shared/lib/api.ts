import axios, { AxiosInstance, InternalAxiosRequestConfig, AxiosError } from 'axios';
import { useUserStore } from '../store/user';

const apiClient: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  withCredentials: true, // send HttpOnly cookies on every request
  headers: { 'Content-Type': 'application/json' },
});

let refreshPromise: Promise<void> | null = null;

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

      if (!refreshPromise) {
        refreshPromise = apiClient
          .post('/auth/refresh')
          .then(() => {})
          .catch(() => {
            useUserStore.getState().clearAuth();
          })
          .finally(() => {
            refreshPromise = null;
          });
      }

      try {
        await refreshPromise;
        return apiClient(originalRequest);
      } catch {
        return Promise.reject(error);
      }
    }

    return Promise.reject(error);
  }
);

export { apiClient };
