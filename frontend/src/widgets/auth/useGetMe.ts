import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { authService, User } from '../../shared/lib/auth';
import { useUserStore } from '../../shared/store/user';

export function useGetMe(options?: { enabled?: boolean }) {
  const { setUser } = useUserStore();

  const query = useQuery<User>({
    queryKey: ['me'],
    queryFn: async () => {
      return await authService.getMe();
    },
    enabled: options?.enabled ?? true,
    retry: false,
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    if (query.data) {
      setUser(query.data);
    }
  }, [query.data, setUser]);

  return query;
}


