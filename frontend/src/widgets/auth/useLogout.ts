import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useUserStore } from '../../shared/store/user';
import { useNavigate } from 'react-router-dom';

export function useLogout() {
  const navigate = useNavigate();
  const { clearAuth, getRefreshToken } = useUserStore();

  return useMutation({
    mutationFn: async () => {
      const refreshToken = getRefreshToken();
      await authService.logout(refreshToken || undefined);
    },
    onSuccess: () => {
      clearAuth();
      navigate('/auth');
    },
    onError: (error) => {
      console.error('Logout error:', error);
      // Clear auth state even if server logout fails
      clearAuth();
      navigate('/auth');
    },
  });
}

