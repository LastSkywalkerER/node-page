import { create } from 'zustand'
import type { ReactNode } from 'react'

// Imperative, promise-based confirmation dialog — the custom replacement for the
// browser's window.confirm/alert/prompt. Works from any handler:
//   const { confirmed } = await confirmDialog({ title, description, variant })
// An optional checkbox covers the two-step confirms (e.g. "also remove hosts?").
//
// USE THIS for every confirmation/destructive prompt (node/connector removal,
// settings applies, raft resets, …) — never window.confirm. The matching
// <ConfirmDialogHost/> (shared/components/ConfirmDialog) is mounted at the app root.

export interface ConfirmOptions {
  title: string
  description?: ReactNode
  confirmText?: string
  cancelText?: string
  variant?: 'default' | 'destructive'
  // Optional extra opt-in surfaced as a checkbox; its state is returned as `checked`.
  checkbox?: { label: ReactNode; defaultChecked?: boolean }
}

export interface ConfirmResult {
  confirmed: boolean
  checked: boolean
}

interface ConfirmState {
  open: boolean
  options: ConfirmOptions | null
  checked: boolean
  resolve: ((r: ConfirmResult) => void) | null
  setChecked: (v: boolean) => void
  close: (confirmed: boolean) => void
  show: (options: ConfirmOptions, resolve: (r: ConfirmResult) => void) => void
}

export const useConfirmStore = create<ConfirmState>((set, get) => ({
  open: false,
  options: null,
  checked: false,
  resolve: null,
  setChecked: (v) => set({ checked: v }),
  close: (confirmed) => {
    const { resolve, checked } = get()
    resolve?.({ confirmed, checked: confirmed ? checked : false })
    set({ open: false, options: null, resolve: null, checked: false })
  },
  show: (options, resolve) =>
    set({ open: true, options, resolve, checked: options.checkbox?.defaultChecked ?? false }),
}))

/** Open the confirm dialog and resolve once the user confirms or cancels. */
export function confirmDialog(options: ConfirmOptions): Promise<ConfirmResult> {
  return new Promise((resolve) => {
    useConfirmStore.getState().show(options, resolve)
  })
}
