import { NavLink } from 'react-router-dom'
import { ConnectorHintDot } from '@/widgets/connectors/ConnectorHint'
import { cn } from '@/lib/utils'

interface AdminTab {
  to: string
  label: string
  end?: boolean
  hint?: boolean
}

const TABS: AdminTab[] = [
  { to: '/admin/users', label: 'Users', end: true },
  { to: '/admin/nodes', label: 'Nodes' },
  { to: '/admin/connectors', label: 'Connectors', hint: true },
  { to: '/admin/gateway', label: 'Gateway' },
  { to: '/admin/server', label: 'Server' },
  { to: '/admin/database', label: 'Database' },
  { to: '/admin/prometheus', label: 'Prometheus' },
  { to: '/admin/logs', label: 'Logs' },
  { to: '/admin/account', label: 'Account' },
]

/**
 * Admin section navigation as a floating glass panel — vertical on md+, a
 * horizontally-scrolling row on small screens. Mirrors the app's glass blocks
 * (header / cards): same border, blur, and cyan glow in dark mode.
 */
export function AdminSidebar() {
  return (
    <nav
      aria-label="Admin sections"
      className={cn(
        'rounded-2xl border p-2',
        'border-border/80 bg-card/55 backdrop-blur-2xl backdrop-saturate-150',
        'dark:border-cyan-500/12 dark:bg-black/40',
        'shadow-[0_8px_32px_-12px_oklch(0_0_0/0.5)]',
        'dark:shadow-[0_0_48px_-12px_oklch(0.72_0.16_195/0.18),0_16px_40px_-20px_oklch(0_0_0/0.55)]',
        'md:sticky md:top-[4.5rem] md:w-52 md:shrink-0 md:self-start'
      )}
    >
      <div className="flex gap-1 overflow-x-auto md:flex-col md:overflow-visible">
        {TABS.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            end={tab.end}
            className="rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
            {({ isActive }) => (
              <span
                className={cn(
                  'relative flex shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium font-display tracking-wide transition-all duration-200 select-none whitespace-nowrap',
                  isActive
                    ? 'bg-primary text-primary-foreground shadow-[0_0_20px_-4px_oklch(0.72_0.16_195/0.45)]'
                    : 'text-muted-foreground hover:text-foreground hover:bg-background/50 dark:hover:bg-white/5'
                )}
              >
                {tab.label}
                {tab.hint && <ConnectorHintDot className="md:ml-auto" />}
              </span>
            )}
          </NavLink>
        ))}
      </div>
    </nav>
  )
}
