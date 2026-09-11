import type { Host } from '@/widgets/hosts/schemas'

/** The admin page that carries every node's settings and its current fault. */
export const NODE_SETTINGS_PATH = '/admin/nodes'

/**
 * Where to send an operator who clicked a machine's fault marker: to the node
 * SETTINGS of the machine that needs the fix, never to a generic page.
 *
 * `local` means this very node — route inside the current app. Otherwise the
 * fix belongs to another machine, so we open ITS web UI. The alert carries the
 * URL that machine answers on right now, which matters because the fault is
 * often that its advertised URL went stale: linking through the stale one
 * would land nowhere. The registered `dashboard_url` is the fallback.
 *
 * With no usable URL we still route locally: this node's settings list shows
 * every host's fault, so the operator at least reads what to do.
 */
export function nodeAlertTarget(host: Host, localHostId?: number): { local: true } | { href: string } {
  const isThisNode = localHostId !== undefined ? host.id === localHostId : host.id === 1
  if (isThisNode) return { local: true }
  const base = (host.node_alert?.node_url || host.dashboard_url || '').replace(/\/+$/, '')
  return base ? { href: base + NODE_SETTINGS_PATH } : { local: true }
}

/** One-line label for compact surfaces: the headline plus what to do. */
export function nodeAlertSummary(host: Host): string {
  const a = host.node_alert
  if (!a) return ''
  return a.action ? `${a.title} — ${a.action}` : a.title
}
