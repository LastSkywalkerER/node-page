import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useUserStore } from '../../shared/store/user';
import { useNavigate } from 'react-router-dom';

export function useLogout() {
  const navigate = useNavigate();
  const { clearAuth } = useUserStore();

  return useMutation({
    mutationFn: () => authService.logout(),
    onSuccess: () => {
      clearAuth();
      navigate('/auth');
    },
    onError: (error) => {
      console.error('Logout error:', error);
      clearAuth();
      navigate('/auth');
    },
  });
}
