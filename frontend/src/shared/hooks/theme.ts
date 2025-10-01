import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import type { ThemeType } from '@/shared/types/metrics';

const THEME_STORAGE_KEY = 'dashboard-theme';
const DEFAULT_THEME: ThemeType = 'glass-aurora';

// Query key for theme
export const themeQueryKey = ['theme'];

// Get theme from localStorage
const getThemeFromStorage = (): ThemeType => {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    if (stored && ['glass-aurora', 'neon-terminal', 'slate-pro', 'cards-flow'].includes(stored)) {
      return stored as ThemeType;
    }
  } catch (error) {
    console.warn('Failed to read theme from localStorage:', error);
  }
  return DEFAULT_THEME;
};

// Save theme to localStorage
const saveThemeToStorage = (theme: ThemeType): void => {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch (error) {
    console.warn('Failed to save theme to localStorage:', error);
  }
};

// Hook to get current theme
export const useThemeQuery = (): ThemeType => {
  const { data } = useQuery({
    queryKey: themeQueryKey,
    queryFn: getThemeFromStorage,
    staleTime: Infinity, // Theme doesn't change often, cache it
    gcTime: Infinity, // Keep in cache permanently
  });

  return data ?? DEFAULT_THEME;
};

// Hook to set theme (mutation)
export const useSetThemeMutation = () => {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (theme: ThemeType) => {
      saveThemeToStorage(theme);
      return Promise.resolve(theme);
    },
    onSuccess: (theme) => {
      // Update the cached query data immediately
      queryClient.setQueryData(themeQueryKey, theme);
    },
  });
};
