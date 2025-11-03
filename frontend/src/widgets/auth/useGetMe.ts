import { useQuery } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useUserStore } from '../../shared/store/user';

export function useGetMe(options?: { enabled?: boolean }) {
  const { setUser } = useUserStore();

  return useQuery({
    queryKey: ['me'],
    queryFn: async () => {
      return await authService.getMe();
    },
    enabled: options?.enabled ?? true,
    retry: false,
    refetchOnWindowFocus: false,
    onSuccess: (user) => {
      setUser(user);
    },
  });
}


