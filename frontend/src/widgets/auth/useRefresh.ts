import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useUserStore } from '../../shared/store/user';

export function useRefresh() {
  const { clearAuth } = useUserStore();

  return useMutation({
    mutationFn: () => authService.refresh(),
    onError: () => {
      clearAuth();
    },
  });
}
