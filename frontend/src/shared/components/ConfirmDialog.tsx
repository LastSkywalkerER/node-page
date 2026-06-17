import { useEffect } from 'react'
import { Button } from '@/components/ui/button'
import { useConfirmStore } from '@/shared/lib/confirmDialog'

/**
 * Mounted once at the app root; renders the active confirm dialog requested via
 * confirmDialog() (see shared/lib/confirmDialog). Closes on Escape or click-away.
 */
export function ConfirmDialogHost() {
  const { open, options, checked, setChecked, close } = useConfirmStore()

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, close])

  if (!open || !options) return null
  const destructive = options.variant === 'destructive'

  return (
    <div className="fixed inset-0 z-[110] flex items-center justify-center p-4">
      {/* click-away cancels */}
      <div className="absolute inset-0 bg-background/70 backdrop-blur-sm" onClick={() => close(false)} />
      <div className="relative w-full max-w-sm space-y-4 rounded-xl bg-popover p-5 text-popover-foreground shadow-xl ring-1 ring-foreground/10">
        <h2 className="font-display text-base tracking-wide">{options.title}</h2>
        {options.description && (
          <div className="whitespace-pre-line text-sm text-muted-foreground">{options.description}</div>
        )}
        {options.checkbox && (
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={checked}
              onChange={(e) => setChecked(e.target.checked)}
              className="mt-0.5"
            />
            <span className="text-muted-foreground">{options.checkbox.label}</span>
          </label>
        )}
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={() => close(false)}>
            {options.cancelText ?? 'Cancel'}
          </Button>
          <Button
            size="sm"
            variant={destructive ? 'destructive' : 'default'}
            onClick={() => close(true)}
          >
            {options.confirmText ?? 'Confirm'}
          </Button>
        </div>
      </div>
    </div>
  )
}
