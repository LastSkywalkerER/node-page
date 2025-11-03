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
    mutationFn: async (data: LoginData) => {
      return await authService.login(data.email, data.password);
    },
    onSuccess: (payload) => {
      // Persist tokens and user, update auth state
      setAuthFromResponse(payload);
      // Redirect to dashboard on successful login
      navigate('/dashboard');
    },
    onError: (error) => {
      console.error('Login error:', error);
      // Error is handled in the component
    },
  });
}

