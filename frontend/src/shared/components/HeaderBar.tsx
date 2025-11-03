import { useUserStore } from '../store/user';
import { Button } from '../ui/button';
import { LogOut, User } from 'lucide-react';
import { useLogout } from '../../widgets/auth/useLogout';

export function HeaderBar() {
  const { user } = useUserStore();
  const logoutMutation = useLogout();

  const handleLogout = () => {
    logoutMutation.mutate();
  };

  return (
    <header className="sticky top-0 z-50 w-full border-b border-slate-700 bg-slate-900/95 backdrop-blur supports-[backdrop-filter]:bg-slate-900/60">
      <div className="container flex h-14 items-center px-4">
        <div className="mr-4 flex">
          <div className="mr-6 flex items-center space-x-2">
            <User className="h-5 w-5" />
            <span className="font-medium text-white">
              {user?.email || 'User'}
            </span>
            {user?.role && (
              <span className="rounded-full bg-blue-600 px-2 py-1 text-xs font-medium text-white">
                {user.role}
              </span>
            )}
          </div>
        </div>

        <div className="flex flex-1 items-center justify-between space-x-2 md:justify-end">
          <nav className="flex items-center space-x-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={handleLogout}
              disabled={logoutMutation.isPending}
              className="text-slate-400 hover:text-white hover:bg-slate-800"
            >
              <LogOut className="mr-2 h-4 w-4" />
              {logoutMutation.isPending ? 'Logging out...' : 'Logout'}
            </Button>
          </nav>
        </div>
      </div>
    </header>
  );
}

