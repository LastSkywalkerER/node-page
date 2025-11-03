import { useMutation } from '@tanstack/react-query';
import { authService } from '../../shared/lib/auth';
import { useNavigate } from 'react-router-dom';
import { useUserStore } from '../../shared/store/user';
import { storageService } from '../../shared/lib/storage';

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
      // Wait for next tick and verify tokens are saved before navigation
      // This prevents race conditions where dashboard components make requests before tokens are ready
      queueMicrotask(() => {
        // Verify tokens are actually in storage before navigating
        const token = storageService.getAuthToken();
        if (token) {
          navigate('/dashboard');
        } else {
          // If tokens aren't ready, wait a bit more
          setTimeout(() => {
            navigate('/dashboard');
          }, 10);
        }
      });
    },
    onError: (error) => {
      console.error('Login error:', error);
      // Error is handled in the component
    },
  });
}

