import { Navigate } from 'react-router-dom';
import { useSetupStatus } from '../../widgets/setup/useSetup';

interface SetupRouteProps {
  children: React.ReactNode;
}

/**
 * SetupRoute guard that redirects to /setup if setup is needed,
 * or redirects away from /setup if setup is already completed
 */
export function SetupRoute({ children }: SetupRouteProps) {
  const { data: statusData, isLoading } = useSetupStatus();

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <div className="text-muted-foreground text-sm">Loading...</div>
      </div>
    );
  }

  // If setup is needed, redirect to setup page
  if (statusData?.setup_needed) {
    return <Navigate to="/setup" replace />;
  }

  // If setup is not needed, allow access to protected routes
  return <>{children}</>;
}

