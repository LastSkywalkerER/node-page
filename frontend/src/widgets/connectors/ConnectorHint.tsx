import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { useUserStore } from '@/shared/store/user'
import { useConnectors, dismissedHints, dismissHint } from './useConnectors'

function useActiveHints() {
  const { user } = useUserStore()
  const isAdmin = user?.role === 'ADMIN'
  const { data } = useConnectors(isAdmin)
  if (!isAdmin) return []
  const dismissed = dismissedHints()
  return (data?.discovered ?? []).filter((h) => !dismissed.includes(h.type))
}

/**
 * Amber notification dot shown while an environment hint (e.g. "running
 * inside Proxmox") awaits the admin's attention. Admin-only; hidden once the
 * connector is configured or the hint dismissed.
 */
export function ConnectorHintDot({ className }: { className?: string }) {
  const hints = useActiveHints()
  if (hints.length === 0) return null
  return (
    <span
      className={cn(
        'inline-block h-1.5 w-1.5 rounded-full bg-amber-400 shadow-[0_0_6px_oklch(0.8_0.14_85/0.7)]',
        className
      )}
      aria-label="Connector suggestion available"
    />
  )
}

const TOAST_SESSION_KEY = 'connectors:hint-toast-shown'

/**
 * One toast per browser session pointing the admin at the Connectors tab when
 * the machine looks like a hypervisor guest.
 */
export function useConnectorHintToast() {
  const hints = useActiveHints()
  const navigate = useNavigate()

  useEffect(() => {
    if (hints.length === 0) return
    if (sessionStorage.getItem(TOAST_SESSION_KEY)) return
    sessionStorage.setItem(TOAST_SESSION_KEY, '1')
    const hint = hints[0]
    toast.info('Proxmox environment detected', {
      description:
        'This machine looks like a Proxmox guest. Connect the Proxmox API to monitor the hypervisor and all of its VMs/LXCs.',
      duration: 12000,
      action: {
        label: 'Connect',
        onClick: () => navigate('/admin/connectors'),
      },
      cancel: {
        label: 'Dismiss',
        onClick: () => dismissHint(hint.type),
      },
    })
  }, [hints, navigate])
}
