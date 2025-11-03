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
    mutationFn: async (data: RegisterData) => {
      return await authService.register(data.email, data.password);
    },
    onSuccess: (payload) => {
      // Persist tokens and user, update auth state
      setAuthFromResponse(payload);
      // Redirect to dashboard on successful registration
      navigate('/dashboard');
    },
    onError: (error) => {
      console.error('Registration error:', error);
      // Error is handled in the component
    },
  });
}

