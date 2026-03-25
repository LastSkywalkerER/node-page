import { Link, NavLink, useParams, useMatch } from 'react-router-dom'
import { Sun, Moon, LogOut, ChevronLeft, LayoutGrid, Settings } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { useTheme } from '@/shared/hooks/useTheme'
import { useUserStore } from '@/shared/store/user'
import { useHosts } from '@/widgets/hosts/useHosts'
import { authService } from '@/shared/lib/auth'
import { cn } from '@/lib/utils'

function MachineNav() {
  const { id } = useParams<{ id: string }>()
  const { data: hostsData } = useHosts()
  const hostName = hostsData?.hosts?.find((h) => h.id === Number(id))?.name ?? `#${id}`

  return (
    <div className="flex items-center gap-2 min-w-0">
      <Link
        to="/machines"
        className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors shrink-0"
      >
        <ChevronLeft className="h-3.5 w-3.5" />
        Machines
      </Link>
      <span className="text-muted-foreground/40 text-xs">/</span>
      <span className="text-sm font-medium truncate max-w-[140px]">{hostName}</span>
      <Separator orientation="vertical" className="h-4 mx-1" />
      <nav className="flex rounded-md overflow-hidden border border-border text-xs">
        <NavLink to={`/machines/${id}/stats`}>
          {({ isActive }) => (
            <span className={cn(
              'px-3 py-1 transition-colors cursor-pointer select-none',
              isActive ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground hover:text-foreground'
            )}>
              Stats
            </span>
          )}
        </NavLink>
        <NavLink to={`/machines/${id}/containers`}>
          {({ isActive }) => (
            <span className={cn(
              'px-3 py-1 border-l border-border transition-colors cursor-pointer select-none',
              isActive ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground hover:text-foreground'
            )}>
              Containers
            </span>
          )}
        </NavLink>
      </nav>
    </div>
  )
}

export function AppHeader() {
  const { theme, toggle } = useTheme()
  const { user, clearAuth } = useUserStore()
  const onMachineDetail = !!useMatch('/machines/:id/*')

  const handleLogout = async () => {
    try { await authService.logout() } finally { clearAuth() }
  }

  return (
    <header className="sticky top-0 z-50 border-b border-border/60 bg-background/90 backdrop-blur-md">
      <div className="mx-auto max-w-7xl px-4 h-11 flex items-center gap-4">
        {/* Logo */}
        <Link
          to="/machines"
          className="flex items-center gap-2 text-sm font-semibold shrink-0 text-foreground/80 hover:text-foreground transition-colors"
        >
          <LayoutGrid className="h-4 w-4" />
          <span className="hidden sm:block tracking-tight">node-stats</span>
        </Link>

        {/* Machine breadcrumb + tabs */}
        {onMachineDetail && (
          <div className="flex-1 min-w-0">
            <MachineNav />
          </div>
        )}

        {/* Spacer */}
        {!onMachineDetail && <div className="flex-1" />}

        {/* Right actions */}
        <div className="flex items-center gap-0.5">
          {user?.role === 'ADMIN' && (
            <Link to="/admin/users">
              <Button variant="ghost" size="icon-sm" aria-label="Admin">
                <Settings className="h-[15px] w-[15px]" />
              </Button>
            </Link>
          )}
          {user && (
            <span className="text-xs text-muted-foreground mr-2 hidden md:block">{user.email}</span>
          )}
          <Button variant="ghost" size="icon-sm" onClick={toggle} aria-label="Toggle theme">
            {theme === 'dark'
              ? <Sun className="h-[15px] w-[15px]" />
              : <Moon className="h-[15px] w-[15px]" />
            }
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={handleLogout} aria-label="Logout">
            <LogOut className="h-[15px] w-[15px]" />
          </Button>
        </div>
      </div>
    </header>
  )
}
