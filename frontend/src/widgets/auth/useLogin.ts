import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useNavigate } from 'react-router-dom';
import { useUserStore } from '../../shared/store/user';

interface LoginData {
  email: string;
  password: string;
}

export function useLogin() {
  const navigate = useNavigate();
  const { setAuthFromResponse } = useUserStore();

  return useMutation({
    mutationFn: async (data: LoginData) => authService.login(data.email, data.password),
    onSuccess: (payload) => {
      setAuthFromResponse(payload);
      navigate('/machines');
    },
    onError: () => {},
  });
}
