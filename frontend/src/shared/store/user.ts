import { create } from 'zustand';
import { User, AuthResponse, authService } from '../lib/auth';

interface UserState {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  error: string | null;

  setUser: (user: User | null) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setAuthFromResponse: (payload: AuthResponse) => void;
  clearAuth: () => void;

  // Schedule a proactive token refresh (expiresIn is in seconds)
  scheduleTokenRefresh: (expiresIn: number) => void;

  // Verify auth state by calling /users/me — used on app startup
  initializeAuth: () => Promise<void>;
}

// Module-level timer handle — lives outside Zustand state to avoid serialization issues
let refreshTimerHandle: ReturnType<typeof setTimeout> | null = null;

function cancelRefreshTimer() {
  if (refreshTimerHandle !== null) {
    clearTimeout(refreshTimerHandle);
    refreshTimerHandle = null;
  }
}

export const useUserStore = create<UserState>((set, get) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,
  error: null,

  setUser: (user) => set({ user, isAuthenticated: !!user }),
  setLoading: (loading) => set({ isLoading: loading }),
  setError: (error) => set({ error }),

  setAuthFromResponse: (payload: AuthResponse) => {
    set({ user: payload.user, isAuthenticated: true });
    get().scheduleTokenRefresh(payload.expires_in);
  },

  clearAuth: () => {
    cancelRefreshTimer();
    set({ user: null, isAuthenticated: false });
  },

  scheduleTokenRefresh: (expiresIn: number) => {
    cancelRefreshTimer();
    // Fire 60 s before the token expires so the refresh happens proactively
    const delayMs = Math.max((expiresIn - 60) * 1000, 0);
    refreshTimerHandle = setTimeout(async () => {
      try {
        const newExpiresIn = await authService.refresh();
        get().scheduleTokenRefresh(newExpiresIn);
      } catch {
        // Refresh token is gone or invalid — force the user to log in again
        get().clearAuth();
      }
    }, delayMs);
  },

  initializeAuth: async () => {
    set({ isLoading: true });
    try {
      const user = await authService.getMe();
      set({ user, isAuthenticated: true, isLoading: false });
      // We don't know the exact remaining TTL of the current access token, so schedule
      // a conservative proactive refresh at 14 min. If the token expires sooner the
      // response interceptor in api.ts will handle the 401 and reschedule correctly.
      get().scheduleTokenRefresh(14 * 60);
    } catch {
      set({ user: null, isAuthenticated: false, isLoading: false });
    }
  },
}));
