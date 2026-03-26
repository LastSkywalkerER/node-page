import { Link, NavLink, useParams, useMatch, useLocation } from 'react-router-dom'
import { Sun, Moon, LogOut, LayoutGrid, Settings } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import { useTheme } from '@/shared/hooks/useTheme'
import { useUserStore } from '@/shared/store/user'
import { useHosts } from '@/widgets/hosts/useHosts'
import { authService } from '@/shared/lib/auth'
import { cn } from '@/lib/utils'

function AdminNav() {
  const { pathname } = useLocation()
  const isNodes = pathname.includes('/admin/nodes')
  const sectionLabel = isNodes ? 'Nodes' : 'Users'

  return (
    <div className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden sm:gap-3">
      <Breadcrumb className="min-w-0 flex-1 overflow-hidden">
        <BreadcrumbList className="flex-nowrap text-xs sm:text-sm">
          <BreadcrumbItem>
            <BreadcrumbLink
              className="cursor-pointer"
              render={(props) => <Link {...props} to="/machines" />}
            >
              Machines
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbLink
              className="cursor-pointer font-display tracking-wide"
              render={(props) => <Link {...props} to="/admin/users" />}
            >
              Admin
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage className="font-medium font-display tracking-wide text-xs sm:text-sm">
              {sectionLabel}
            </BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <nav
        className={cn(
          'ml-auto flex shrink-0 rounded-lg overflow-hidden border p-0.5 gap-0.5',
          'border-border/70 bg-muted/35 backdrop-blur-md',
          'dark:border-cyan-500/15 dark:bg-black/35 dark:shadow-[0_0_24px_-8px_oklch(0.72_0.16_195/0.2)]'
        )}
        aria-label="Admin sections"
      >
        <NavLink
          to="/admin/users"
          end
          className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          {({ isActive }) => (
            <span
              className={cn(
                'px-3 py-1 rounded-md transition-all duration-200 cursor-pointer select-none text-xs font-medium inline-block',
                isActive
                  ? 'bg-primary text-primary-foreground shadow-[0_0_20px_-4px_oklch(0.72_0.16_195/0.45)]'
                  : 'text-muted-foreground hover:text-foreground hover:bg-background/50 dark:hover:bg-white/5'
              )}
            >
              Users
            </span>
          )}
        </NavLink>
        <NavLink
          to="/admin/nodes"
          className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          {({ isActive }) => (
            <span
              className={cn(
                'px-3 py-1 rounded-md transition-all duration-200 cursor-pointer select-none text-xs font-medium inline-block',
                isActive
                  ? 'bg-primary text-primary-foreground shadow-[0_0_20px_-4px_oklch(0.72_0.16_195/0.45)]'
                  : 'text-muted-foreground hover:text-foreground hover:bg-background/50 dark:hover:bg-white/5'
              )}
            >
              Nodes
            </span>
          )}
        </NavLink>
      </nav>
    </div>
  )
}

function MachineNav() {
  const { id } = useParams<{ id: string }>()
  const { pathname } = useLocation()
  const { data: hostsData } = useHosts()
  const hostName = hostsData?.hosts?.find((h) => h.id === Number(id))?.name ?? `#${id}`
  const isContainers = pathname.endsWith('/containers')
  const sectionLabel = isContainers ? 'Containers' : 'Metrics'

  return (
    <div className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden sm:gap-3">
      <Breadcrumb className="min-w-0 flex-1 overflow-hidden">
        <BreadcrumbList className="flex-nowrap text-xs sm:text-sm">
          <BreadcrumbItem>
            <BreadcrumbLink
              className="cursor-pointer"
              render={(props) => <Link {...props} to="/machines" />}
            >
              Machines
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem className="min-w-0">
            <BreadcrumbLink
              className="cursor-pointer truncate max-w-[160px] sm:max-w-[220px] font-display tracking-wide"
              render={(props) => <Link {...props} to={`/machines/${id}/stats`} />}
            >
              {hostName}
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage className="font-medium font-display tracking-wide text-xs sm:text-sm">
              {sectionLabel}
            </BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <nav
        className={cn(
          'ml-auto flex shrink-0 rounded-lg overflow-hidden border p-0.5 gap-0.5',
          'border-border/70 bg-muted/35 backdrop-blur-md',
          'dark:border-cyan-500/15 dark:bg-black/35 dark:shadow-[0_0_24px_-8px_oklch(0.72_0.16_195/0.2)]'
        )}
        aria-label="Machine sections"
      >
        <NavLink
          to={`/machines/${id}/stats`}
          className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          {({ isActive }) => (
            <span
              className={cn(
                'px-3 py-1 rounded-md transition-all duration-200 cursor-pointer select-none text-xs font-medium inline-block',
                isActive
                  ? 'bg-primary text-primary-foreground shadow-[0_0_20px_-4px_oklch(0.72_0.16_195/0.45)]'
                  : 'text-muted-foreground hover:text-foreground hover:bg-background/50 dark:hover:bg-white/5'
              )}
            >
              Stats
            </span>
          )}
        </NavLink>
        <NavLink
          to={`/machines/${id}/containers`}
          className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          {({ isActive }) => (
            <span
              className={cn(
                'px-3 py-1 rounded-md transition-all duration-200 cursor-pointer select-none text-xs font-medium inline-block',
                isActive
                  ? 'bg-primary text-primary-foreground shadow-[0_0_20px_-4px_oklch(0.72_0.16_195/0.45)]'
                  : 'text-muted-foreground hover:text-foreground hover:bg-background/50 dark:hover:bg-white/5'
              )}
            >
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
  const onAdmin = !!useMatch('/admin/*')

  const handleLogout = async () => {
    try { await authService.logout() } finally { clearAuth() }
  }

  return (
    <header
      className={cn(
        'sticky top-3 z-50 w-full rounded-2xl md:top-4',
        'border border-border/80 bg-card/55 backdrop-blur-2xl backdrop-saturate-150',
        'dark:border-cyan-500/12 dark:bg-black/40',
        'shadow-[0_8px_32px_-12px_oklch(0_0_0/0.5)]',
        'dark:shadow-[0_0_48px_-12px_oklch(0.72_0.16_195/0.18),0_16px_40px_-20px_oklch(0_0_0/0.55)]'
      )}
    >
      <div className="mx-auto flex h-12 max-w-7xl min-w-0 items-center gap-2 px-3 sm:h-[52px] sm:gap-3 sm:px-4">
        <Link
          to="/machines"
          className="flex shrink-0 items-center gap-2 text-sm font-semibold font-display tracking-wide text-foreground/90 transition-colors duration-200 hover:text-primary cursor-pointer"
        >
          <LayoutGrid className="h-4 w-4 text-primary" />
          <span className="hidden sm:inline uppercase text-xs">node-stats</span>
        </Link>

        {onMachineDetail && <MachineNav />}
        {onAdmin && <AdminNav />}

        {!onMachineDetail && !onAdmin && <div className="hidden min-w-0 flex-1 sm:block" />}

        <div className="ml-auto flex shrink-0 items-center gap-0.5">
          {user?.role === 'ADMIN' && (
            <Tooltip>
              <TooltipTrigger
                delay={400}
                render={(
                  <Link
                    to="/admin/users"
                    aria-label="Administration"
                    className="inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-lg text-foreground outline-none transition-colors hover:bg-muted hover:text-foreground dark:hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                  />
                )}
              >
                <Settings className="h-[15px] w-[15px]" />
              </TooltipTrigger>
              <TooltipContent side="bottom">Users and cluster admin</TooltipContent>
            </Tooltip>
          )}
          {user && (
            <span className="text-xs text-muted-foreground mr-2 hidden lg:block max-w-[200px] truncate font-mono">
              {user.email}
            </span>
          )}
          <Tooltip>
            <TooltipTrigger
              delay={400}
              render={(
                <Button variant="ghost" size="icon-sm" onClick={toggle} aria-label="Toggle theme" />
              )}
            >
              {theme === 'dark'
                ? <Sun className="h-[15px] w-[15px]" />
                : <Moon className="h-[15px] w-[15px]" />
              }
            </TooltipTrigger>
            <TooltipContent side="bottom">
              {theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger
              delay={400}
              render={(
                <Button variant="ghost" size="icon-sm" onClick={handleLogout} aria-label="Log out" />
              )}
            >
              <LogOut className="h-[15px] w-[15px]" />
            </TooltipTrigger>
            <TooltipContent side="bottom">Log out</TooltipContent>
          </Tooltip>
        </div>
      </div>
    </header>
  )
}
