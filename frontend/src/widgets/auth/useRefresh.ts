import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useUserStore } from '../../shared/store/user';

export function useRefresh() {
  const { setTokensFromRefresh, clearAuth, getRefreshToken } = useUserStore();

  return useMutation({
    mutationFn: async () => {
      const refreshToken = getRefreshToken();
      if (!refreshToken) {
        throw new Error('No refresh token available');
      }
      return await authService.refresh(refreshToken);
    },
    onSuccess: (payload) => {
      setTokensFromRefresh(payload);
    },
    onError: () => {
      // On refresh failure clear auth state
      clearAuth();
    },
  });
}


