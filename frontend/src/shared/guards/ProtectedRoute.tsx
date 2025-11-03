import { Navigate } from 'react-router-dom';
import { useUserStore } from '../store/user';
import { storageService } from '../lib/storage';

interface ProtectedRouteProps {
  children: React.ReactNode;
}

export function ProtectedRoute({ children }: ProtectedRouteProps) {
  const { getRefreshToken } = useUserStore();
  
  // Check if we have a valid refresh token
  // Access token can be expired - interceptor will refresh it automatically
  const hasRefreshToken = !!getRefreshToken() && !storageService.isRefreshTokenExpired();

  if (!hasRefreshToken) {
    return <Navigate to="/auth" replace />;
  }

  return <>{children}</>;
}

