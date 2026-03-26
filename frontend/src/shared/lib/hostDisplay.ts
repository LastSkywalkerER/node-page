import type { Host } from '@/widgets/hosts/schemas'

/**
 * Machine card title: optional API `display_name` (NODE_STATS_HOSTNAME) overrides registered `name`.
 */
export function getHostCardTitle(host: Host): string | null {
  const label = host.display_name?.trim() || host.name?.trim()
  return label || null
}

/**
 * Breadcrumb label: same rule as the card — env display override, else stored hostname.
 */
export function getHostNavLabel(host: Host): string {
  return getHostCardTitle(host) ?? `Host ${host.id}`
}
