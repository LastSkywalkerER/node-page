import { Navigate, Outlet } from 'react-router-dom'
import { useUserStore } from '../store/user'

/**
 * Guard that redirects non-admin users to the dashboard.
 * Must be used inside ProtectedRoute (user is authenticated).
 */
export function AdminRoute() {
  const { user } = useUserStore()

  if (!user) {
    return <Navigate to="/auth" replace />
  }

  if (user.role !== 'ADMIN') {
    return <Navigate to="/" replace />
  }

  return <Outlet />
}
