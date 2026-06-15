import { useState } from 'react'
import { ArrowUpCircle } from 'lucide-react'
import { Switch } from '@/shared/ui/switch'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { useRestartStore } from '@/shared/lib/restartStore'
import {
  useVersion,
  useSetAutoUpdate,
  useUpdateNow,
  useRefreshVersion,
  useDeployWebhook,
  useSetDeployWebhook,
  useSetReleaseChannel,
} from './useVersion'

/**
 * Admin-only header control: surfaces the build version, an "update available"
 * affordance, the auto-update toggle, and a manual "Update now" button. Render
 * only for admins (the parent gates on role).
 */
export function UpdateBadge() {
  const { data: v } = useVersion()
  const setAuto = useSetAutoUpdate()
  const setChannel = useSetReleaseChannel()
  const updateNow = useUpdateNow()
  const refresh = useRefreshVersion()
  const [open, setOpen] = useState(false)
  const [msg, setMsg] = useState<string | null>(null)
  // Orchestrator deploy webhook (managed-externally only). null = untouched →
  // show the server-side value; a string = the operator's in-progress edit.
  const webhook = useDeployWebhook(open && !!v?.managed_externally)
  const saveWebhook = useSetDeployWebhook()
  const [webhookDraft, setWebhookDraft] = useState<string | null>(null)

  if (!v) return null
  const hasUpdate = !!v.update_available
  const canApply = !v.managed_externally || !!v.deploy_webhook_configured
  const savedWebhookUrl = webhook.data?.url ?? ''
  const webhookValue = webhookDraft ?? savedWebhookUrl

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() =>
          setOpen((o) => {
            // Opening the popup re-checks the latest release (rate-limited
            // server-side) so it never shows the stale hourly cache.
            if (!o) refresh.mutate()
            return !o
          })
        }
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
              {v.commit && (
                <span className="font-mono text-foreground/70"> ·{v.commit.slice(0, 7)}</span>
              )}
              {v.latest && (
                <>
                  {' · '}Latest <span className="font-mono text-foreground">{v.latest}</span>
                </>
              )}
            </div>

            {hasUpdate && canApply && (
              <Button
                size="sm"
                className="w-full"
                disabled={updateNow.isPending}
                onClick={async () => {
                  setMsg(null)
                  try {
                    const r = await updateNow.mutateAsync()
                    setMsg(r.message)
                    // The update was already triggered server-side (controller
                    // pull+recreate / orchestrator webhook); just poll until back.
                    setOpen(false)
                    useRestartStore.getState().show('poll', 'update')
                  } catch (e) {
                    setMsg(e instanceof Error ? e.message : 'update failed')
                  }
                }}
              >
                {updateNow.isPending ? 'Updating…' : `Update to ${v.latest}`}
              </Button>
            )}

            {v.managed_externally && (
              <div className="space-y-1">
                <label htmlFor="deploy-webhook" className="text-xs">
                  Deploy webhook URL
                </label>
                <div className="flex items-center gap-1">
                  <Input
                    id="deploy-webhook"
                    placeholder="https://dokploy…/api/deploy/compose/…"
                    value={webhookValue}
                    onChange={(e) => setWebhookDraft(e.target.value)}
                    className="h-7 text-xs"
                  />
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-7 px-2"
                    disabled={
                      saveWebhook.isPending ||
                      webhookDraft === null ||
                      webhookDraft.trim() === savedWebhookUrl
                    }
                    onClick={async () => {
                      setMsg(null)
                      try {
                        await saveWebhook.mutateAsync(webhookValue.trim())
                        setWebhookDraft(null)
                        setMsg('Webhook saved.')
                      } catch (e) {
                        setMsg(e instanceof Error ? e.message : 'save failed')
                      }
                    }}
                  >
                    Save
                  </Button>
                </div>
              </div>
            )}

            <div className="flex items-center justify-between gap-2">
              <span className="text-xs">Channel</span>
              <div className="inline-flex overflow-hidden rounded-md border border-border/70 text-[0.7rem]">
                {(['stable', 'beta'] as const).map((ch) => (
                  <button
                    key={ch}
                    type="button"
                    disabled={setChannel.isPending || v.channel === ch}
                    onClick={() => {
                      setMsg(null)
                      setChannel.mutate(ch)
                    }}
                    className={cn(
                      'px-2 py-0.5 capitalize transition-colors disabled:cursor-default',
                      v.channel === ch
                        ? 'bg-primary text-primary-foreground'
                        : 'text-muted-foreground hover:bg-muted',
                    )}
                  >
                    {ch}
                  </button>
                ))}
              </div>
            </div>

            {v.managed_externally && (
              <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-2 py-1.5 text-[0.65rem] leading-relaxed text-amber-600 dark:text-amber-400">
                The actual image is set by your orchestrator’s{' '}
                <span className="font-mono">NODE_STATS_IMAGE</span> tag — to run beta, set it to{' '}
                <span className="font-mono">…:beta</span> in dokploy and redeploy. This toggle only
                changes which line the updater watches, not what gets deployed.
              </p>
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
              {v.managed_externally
                ? v.deploy_webhook_configured
                  ? 'Managed externally: updates trigger the orchestrator’s deploy webhook (pulls the latest image and redeploys).'
                  : 'Managed by an external orchestrator (e.g. dokploy): paste its deploy-webhook URL (Deployments tab) to update from here.'
                : v.deployment === 'native'
                  ? 'Native install: runs `node-stats update` to self-replace the binary.'
                  : 'Docker: pulls the new image and recreates the stack via the controller.'}
            </p>

            {msg && <p className="text-[0.65rem] leading-relaxed text-emerald-400">{msg}</p>}
            {!hasUpdate &&
              (v.channel === 'beta' ? (
                <p className="text-[0.65rem] text-muted-foreground">
                  Beta channel — tracking the rolling <span className="font-mono">main</span> build
                  (use “Update to {v.latest}” / Stable for a tagged release).
                </p>
              ) : (
                <p className="text-[0.65rem] text-muted-foreground">You're on the latest release.</p>
              ))}
          </div>
        </>
      )}
    </div>
  )
}
