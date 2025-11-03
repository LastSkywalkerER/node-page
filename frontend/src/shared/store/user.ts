import { create } from 'zustand';
import { User, AuthResponse, RefreshResponse } from '../lib/auth';
import { storageService } from '../lib/storage';

interface UserState {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  error: string | null;

  // Actions
  setUser: (user: User | null) => void;
  setAuthenticated: (authenticated: boolean) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setAuthFromResponse: (payload: AuthResponse) => void;
  clearAuth: () => void;
  setTokensFromRefresh: (payload: RefreshResponse) => void;

  // Getters for tokens (from storage)
  getRefreshToken: () => string | null;
  getAccessToken: () => string | null;
  hasValidTokens: () => boolean;

  // Initialize from storage (sync, no API calls)
  initializeFromStorage: () => void;
}

export const useUserStore = create<UserState>((set, get) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,
  error: null,

  setUser: (user) => {
    if (user) {
      storageService.setUser(user);
      set({ user, isAuthenticated: true });
    } else {
      storageService.clearAll();
      set({ user: null, isAuthenticated: false });
    }
  },
  setAuthenticated: (authenticated) => set({ isAuthenticated: authenticated }),
  setLoading: (loading) => set({ isLoading: loading }),
  setError: (error) => set({ error }),

  getRefreshToken: () => storageService.getRefreshToken(),
  getAccessToken: () => storageService.getAuthToken(),
  hasValidTokens: () => storageService.hasValidTokens(),

  setAuthFromResponse: (payload: AuthResponse) => {
    // Persist tokens and user to storage
    const accessExp = Math.floor((Date.now() + payload.expires_in * 1000) / 1000);
    const refreshExp = Math.floor((Date.now() + 30 * 24 * 60 * 60 * 1000) / 1000); // 30 days
    storageService.setAccessToken(payload.access_token, accessExp);
    storageService.setRefreshToken(payload.refresh_token, refreshExp);
    storageService.setUser(payload.user);
    set({ user: payload.user, isAuthenticated: true });
  },

  clearAuth: () => {
    storageService.clearAll();
    set({ user: null, isAuthenticated: false });
  },

  setTokensFromRefresh: (payload: RefreshResponse) => {
    const accessExp = Math.floor((Date.now() + payload.expires_in * 1000) / 1000);
    const refreshExp = Math.floor((Date.now() + 30 * 24 * 60 * 60 * 1000) / 1000);
    storageService.setAccessToken(payload.access_token, accessExp);
    storageService.setRefreshToken(payload.refresh_token, refreshExp);
    // Keep existing user if present; mark authenticated
    set((state) => ({ isAuthenticated: true, user: state.user }));
  },

  initializeFromStorage: () => {
    const hasValid = storageService.hasValidTokens();
    if (hasValid) {
      const user = storageService.getUser();
      if (user) {
        set({ user, isAuthenticated: true, isLoading: false });
      } else {
        set({ user: null, isAuthenticated: false, isLoading: false });
      }
    } else {
      set({ user: null, isAuthenticated: false, isLoading: false });
    }
  },
}));

