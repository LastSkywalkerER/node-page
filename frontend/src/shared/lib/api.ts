import axios, { AxiosInstance, InternalAxiosRequestConfig } from 'axios';
import { storageService } from './storage';

// Create axios instance
const apiClient: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  headers: {
    'Content-Type': 'application/json',
  },
});

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

// Response interceptor: pass through responses and errors without any logic
apiClient.interceptors.response.use(
  (response) => response,
  (error) => Promise.reject(error)
);

export { apiClient };

