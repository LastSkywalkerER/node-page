import axios, { AxiosInstance, InternalAxiosRequestConfig, AxiosError, AxiosResponse } from 'axios';
import { useUserStore } from '../store/user';

const apiClient: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  withCredentials: true, // send HttpOnly cookies on every request
  headers: { 'Content-Type': 'application/json' },
  timeout: 15000,
});

/**
 * Every API handler answers with a `{ "data": ... }` envelope. Reading
 * `response.data.data` blindly turns any *other* 2xx body into a cryptic
 * `Cannot read properties of undefined` TypeError further down the call chain
 * (the login form used to print exactly that). A 2xx that isn't our envelope
 * means the response never reached the browser intact — a captive portal, a
 * carrier/corporate proxy, an ad-blocking VPN or a stale cached page answered
 * instead — which is why it can happen on one device (phone/mobile network)
 * while another works fine. Unwrap defensively and say what actually arrived.
 */
export function unwrapEnvelope<T>(response: AxiosResponse<unknown>, what: string): T {
  const body = response.data;
  if (body && typeof body === 'object' && 'data' in (body as Record<string, unknown>)) {
    return (body as { data: T }).data;
  }
  const contentType = String(response.headers?.['content-type'] ?? 'unknown content type');
  const preview =
    typeof body === 'string' ? body.slice(0, 200) : JSON.stringify(body ?? null).slice(0, 200);
  console.error(`Unexpected ${what} response`, {
    status: response.status,
    contentType,
    body: preview,
  });
  throw new Error(
    `Unexpected server response to ${what} (HTTP ${response.status}, ${contentType}). ` +
      'The request did not reach node-stats intact — a proxy, VPN, ad blocker or a stale ' +
      'cached page may be intercepting it. Reload the page, and if it persists clear this ' +
      "site's data in the browser or retry on another network.",
  );
}

/** Human-readable message for a failed request: server `error` field, else the axios message. */
export function apiErrorMessage(error: unknown, fallback = 'Request failed'): string {
  const ax = error as AxiosError<{ error?: string; detail?: string }> | undefined;
  return ax?.response?.data?.error || ax?.response?.data?.detail || ax?.message || fallback;
}

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
        const expiresIn = unwrapEnvelope<{ expires_in: number }>(res, 'token refresh')?.expires_in ?? 0;
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
