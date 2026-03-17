import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useNavigate } from 'react-router-dom';
import { useUserStore } from '../../shared/store/user';

interface RegisterData {
  email: string;
  password: string;
}

export function useRegister() {
  const navigate = useNavigate();
  const { setAuthFromResponse } = useUserStore();

  return useMutation({
    mutationFn: async (data: RegisterData) => authService.register(data.email, data.password),
    onSuccess: (payload) => {
      setAuthFromResponse(payload);
      navigate('/dashboard');
    },
    onError: (error) => {
      console.error('Registration error:', error);
    },
  });
}
