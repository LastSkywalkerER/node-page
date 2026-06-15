import { create } from 'zustand'

// How a pending change is applied — mirrors the backend apply_mode, plus a
// client-only "poll" mode for actions that already triggered the restart
// server-side (e.g. "update now") and just need progress polling.
export type ApplyMode = 'controller' | 'webhook' | 'manual' | 'poll'

interface RestartState {
  open: boolean
  mode: ApplyMode | null
  // Human label for what's being applied (e.g. "Prometheus settings", "Update").
  what: string
  show: (mode: ApplyMode, what?: string) => void
  hide: () => void
}

// Drives the shared restart-approval / progress modal. Any settings change or
// update that needs the app to restart calls show(apply_mode); the modal then
// triggers the redeploy (webhook) or waits (controller) and polls until the app
// is back.
export const useRestartStore = create<RestartState>((set) => ({
  open: false,
  mode: null,
  what: 'changes',
  show: (mode, what = 'changes') => set({ open: true, mode, what }),
  hide: () => set({ open: false, mode: null }),
}))

// notifyRestartFromResult opens the modal based on a settings/update response
// that carries apply_mode + restart flags. No-op when nothing needs a restart.
export function notifyRestartFromResult(
  r: { apply_mode?: string; restart_pending?: boolean; restart_required?: boolean },
  what?: string,
) {
  const mode = (r.apply_mode as ApplyMode) || 'manual'
  if (r.restart_pending || r.restart_required) {
    useRestartStore.getState().show(mode, what)
  }
}
