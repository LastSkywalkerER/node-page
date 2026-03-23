import { Outlet, NavLink } from 'react-router-dom'
import { Settings, Users, Server } from 'lucide-react'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/admin/users', icon: Users, label: 'Users' },
  { to: '/admin/nodes', icon: Server, label: 'Nodes' },
]

export function AdminPage() {
  return (
    <div className="flex min-h-[calc(100vh-2.75rem)]">
      {/* Sidebar */}
      <aside className="w-52 shrink-0 border-r border-border bg-muted/20">
        <div className="p-4 border-b border-border">
          <div className="flex items-center gap-2">
            <Settings className="h-5 w-5 text-muted-foreground" />
            <span className="font-semibold text-sm">Admin</span>
          </div>
        </div>
        <nav className="p-2">
          {navItems.map(({ to, icon: Icon, label }) => (
            <NavLink
              key={to}
              to={to}
              end={to === '/admin/users'}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors',
                  isActive
                    ? 'bg-primary text-primary-foreground'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                )
              }
            >
              <Icon className="h-4 w-4 shrink-0" />
              {label}
            </NavLink>
          ))}
        </nav>
      </aside>

      {/* Main content */}
      <main className="flex-1 overflow-auto">
        <div className="p-6 max-w-4xl">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
