import { useState } from 'react'
import { ArrowUpCircle } from 'lucide-react'
import { Switch } from '@/shared/ui/switch'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { useVersion, useSetAutoUpdate, useUpdateNow } from './useVersion'

/**
 * Admin-only header control: surfaces the build version, an "update available"
 * affordance, the auto-update toggle, and a manual "Update now" button. Render
 * only for admins (the parent gates on role).
 */
export function UpdateBadge() {
  const { data: v } = useVersion()
  const setAuto = useSetAutoUpdate()
  const updateNow = useUpdateNow()
  const [open, setOpen] = useState(false)
  const [msg, setMsg] = useState<string | null>(null)

  if (!v) return null
  const hasUpdate = !!v.update_available

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-label={hasUpdate ? `Update available: ${v.latest}` : 'Version & updates'}
        className={cn(
          'inline-flex items-center gap-1 rounded-lg px-2 py-1 text-xs outline-none transition-colors hover:bg-muted dark:hover:bg-muted/50',
          hasUpdate ? 'text-emerald-400' : 'text-muted-foreground',
        )}
      >
        <ArrowUpCircle className="h-[15px] w-[15px]" />
        {hasUpdate && <span className="hidden sm:inline font-mono">{v.latest}</span>}
      </button>

      {open && (
        <>
          {/* click-away */}
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-full z-50 mt-1 w-64 space-y-3 rounded-lg bg-popover p-3 text-sm text-popover-foreground shadow-lg ring-1 ring-foreground/10 backdrop-blur-xl">
            <div className="text-xs text-muted-foreground">
              Current <span className="font-mono text-foreground">{v.current}</span>
              {v.latest && (
                <>
                  {' · '}Latest <span className="font-mono text-foreground">{v.latest}</span>
                </>
              )}
            </div>

            {hasUpdate && (
              <Button
                size="sm"
                className="w-full"
                disabled={updateNow.isPending}
                onClick={async () => {
                  setMsg(null)
                  try {
                    const r = await updateNow.mutateAsync()
                    setMsg(r.message)
                  } catch (e) {
                    setMsg(e instanceof Error ? e.message : 'update failed')
                  }
                }}
              >
                {updateNow.isPending ? 'Updating…' : `Update to ${v.latest}`}
              </Button>
            )}

            <div className="flex items-center justify-between gap-2">
              <span className="text-xs">Auto-update</span>
              <Switch
                id="auto-update"
                checked={!!v.auto_update}
                disabled={setAuto.isPending}
                onCheckedChange={(val) => setAuto.mutate(val)}
              />
            </div>

            <p className="text-[0.65rem] leading-relaxed text-muted-foreground">
              {v.deployment === 'native'
                ? 'Native install: runs `node-stats update` to self-replace the binary.'
                : 'Docker: pulls the new image and recreates the stack via the controller.'}
            </p>

            {msg && <p className="text-[0.65rem] leading-relaxed text-emerald-400">{msg}</p>}
            {!hasUpdate && (
              <p className="text-[0.65rem] text-muted-foreground">You're on the latest release.</p>
            )}
          </div>
        </>
      )}
    </div>
  )
}
