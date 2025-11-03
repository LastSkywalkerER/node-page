import axios, { AxiosInstance, InternalAxiosRequestConfig, AxiosError } from 'axios';
import { storageService } from './storage';
import { authService } from './auth';
import { useUserStore } from '../store/user';

// Create axios instance
const apiClient: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  headers: {
    'Content-Type': 'application/json',
  },
});

// Track refresh token promise to prevent multiple simultaneous refresh requests
let refreshTokenPromise: Promise<void> | null = null;

// Request interceptor to add auth token from storage
apiClient.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const token = storageService.getAuthToken();
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

// Response interceptor: handle 401 errors with automatic token refresh
apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean };

    // Skip refresh logic for auth endpoints to avoid infinite loops
    if (originalRequest?.url?.includes('/auth/refresh') || originalRequest?.url?.includes('/auth/login') || originalRequest?.url?.includes('/auth/register')) {
      return Promise.reject(error);
    }

    // If error is 401 and we haven't already tried to refresh
    if (error.response?.status === 401 && !originalRequest._retry) {
      originalRequest._retry = true;

      const { getRefreshToken, clearAuth, setTokensFromRefresh } = useUserStore.getState();
      const refreshToken = getRefreshToken();
      
      // If no refresh token or it's expired, clear auth and reject
      if (!refreshToken || storageService.isRefreshTokenExpired()) {
        clearAuth();
        // Trigger navigation via store update (React Router will handle it)
        return Promise.reject(error);
      }

      // If refresh is already in progress, wait for it
      if (refreshTokenPromise) {
        try {
          await refreshTokenPromise;
          // Retry original request with new token
          const newToken = storageService.getAuthToken();
          if (newToken && originalRequest.headers) {
            originalRequest.headers.Authorization = `Bearer ${newToken}`;
          }
          return apiClient(originalRequest);
        } catch {
          return Promise.reject(error);
        }
      }

      // Start refresh process
      refreshTokenPromise = (async () => {
        try {
          const response = await authService.refresh(refreshToken);
          // Update both storage and store
          setTokensFromRefresh(response);
        } catch (refreshError) {
          // Refresh failed, clear auth
          clearAuth();
          throw refreshError;
        } finally {
          refreshTokenPromise = null;
        }
      })();

      try {
        await refreshTokenPromise;
        // Retry original request with new token
        const newToken = storageService.getAuthToken();
        if (newToken && originalRequest.headers) {
          originalRequest.headers.Authorization = `Bearer ${newToken}`;
        }
        return apiClient(originalRequest);
      } catch {
        return Promise.reject(error);
      }
    }

    return Promise.reject(error);
  }
);

export { apiClient };

