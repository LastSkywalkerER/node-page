import { useEffect, useRef, useState } from 'react'
import { Loader2, RotateCw, CheckCircle2, AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { apiClient } from '@/shared/lib/api'
import { useRestartStore } from '@/shared/lib/restartStore'

type Phase = 'triggering' | 'waiting' | 'back' | 'manual' | 'error'

// Polls /version (bypassing the axios auth interceptors) to detect the app
// going down and coming back up during a restart/redeploy.
async function pingUp(): Promise<boolean> {
  try {
    const res = await fetch('/api/v1/version', { cache: 'no-store' })
    return res.ok
  } catch {
    return false
  }
}

export function RestartProgressModal() {
  const { open, mode, what, hide } = useRestartStore()
  const [phase, setPhase] = useState<Phase>('waiting')
  const [elapsed, setElapsed] = useState(0)
  const [errMsg, setErrMsg] = useState('')
  const sawDown = useRef(false)

  useEffect(() => {
    if (!open || !mode) return
    let cancelled = false
    sawDown.current = false
    setElapsed(0)
    setErrMsg('')

    if (mode === 'manual') {
      setPhase('manual')
      return
    }

    const start = Date.now()
    const tick = setInterval(() => setElapsed(Math.floor((Date.now() - start) / 1000)), 1000)

    const run = async () => {
      // managed-externally → trigger the orchestrator deploy webhook first.
      if (mode === 'webhook') {
        setPhase('triggering')
        try {
          await apiClient.post('/settings/redeploy', {})
        } catch (e) {
          if (!cancelled) {
            setErrMsg(e instanceof Error ? e.message : 'redeploy failed')
            setPhase('error')
          }
          return
        }
      }
      if (cancelled) return
      setPhase('waiting')

      // Poll until the app goes down (confirming the restart began) and returns.
      while (!cancelled) {
        const up = await pingUp()
        if (!up) {
          sawDown.current = true
        } else if (sawDown.current) {
          if (!cancelled) setPhase('back')
          return
        }
        await new Promise((r) => setTimeout(r, 2500))
      }
    }
    run()

    return () => {
      cancelled = true
      clearInterval(tick)
    }
  }, [open, mode])

  if (!open || !mode) return null

  const title =
    mode === 'webhook'
      ? 'Redeploying via your orchestrator'
      : mode === 'poll'
        ? 'Applying update'
        : mode === 'controller'
          ? 'Recreating the container'
          : 'Restart required'

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-background/70 backdrop-blur-sm" />
      <div className="relative w-full max-w-sm space-y-4 rounded-xl bg-popover p-5 text-popover-foreground shadow-xl ring-1 ring-foreground/10">
        <div className="flex items-center gap-2">
          {phase === 'back' ? (
            <CheckCircle2 className="h-5 w-5 text-emerald-500" />
          ) : phase === 'error' || phase === 'manual' ? (
            <AlertTriangle className="h-5 w-5 text-amber-500" />
          ) : (
            <Loader2 className="h-5 w-5 animate-spin text-primary" />
          )}
          <h2 className="font-display text-base tracking-wide">{title}</h2>
        </div>

        {phase === 'manual' && (
          <p className="text-sm text-muted-foreground">
            Your {what} were saved. Restart the app (or redeploy in your orchestrator) to apply them.
          </p>
        )}

        {(phase === 'triggering' || phase === 'waiting') && (
          <p className="text-sm text-muted-foreground">
            Applying your {what}. The app will go offline briefly while it{' '}
            {mode === 'webhook' ? 'redeploys' : 'restarts'} — this page reconnects automatically.{' '}
            <span className="font-mono tabular-nums">({elapsed}s)</span>
          </p>
        )}

        {phase === 'error' && (
          <p className="text-sm text-destructive">
            Couldn&apos;t trigger the redeploy: {errMsg || 'unknown error'}. Redeploy manually in your
            orchestrator.
          </p>
        )}

        {phase === 'back' && (
          <p className="text-sm text-muted-foreground">The app is back online. Reload to continue.</p>
        )}

        {/* No controller (external manager / native): the app can't mutate the
            compose, so structural changes (DB engine, ports, volumes) must be
            reflected there by hand before the redeploy takes effect. */}
        {(mode === 'webhook' || mode === 'manual') && phase !== 'back' && (
          <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-600 dark:text-amber-400">
            No controller is running (managed externally). If this change touches the database engine,
            ports or volumes, update your compose to match <strong>before</strong> redeploying —
            otherwise it boots back on the old configuration.
          </p>
        )}

        <div className="flex justify-end gap-2">
          {phase === 'back' ? (
            <Button size="sm" onClick={() => window.location.reload()}>
              <RotateCw className="mr-1.5 h-4 w-4" />
              Reload
            </Button>
          ) : phase === 'manual' || phase === 'error' ? (
            <Button size="sm" variant="outline" onClick={hide}>
              Close
            </Button>
          ) : (
            <Button size="sm" variant="ghost" onClick={hide}>
              Dismiss
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
