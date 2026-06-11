/**
 * detectPublicUrl returns the browser's origin when this page is being
 * accessed through a real DNS hostname — i.e. the operator already reaches
 * this node via a public domain / reverse proxy, which is exactly the URL
 * cluster peers should use for HTTP join/forward calls.
 *
 * Returns '' when the address gives no useful signal and the auto-detected
 * LAN address is the better default:
 *  - IP literals (http://192.168.0.104:9090) — same as auto-detection;
 *  - localhost / *.localhost / host.docker.internal — meaningless to peers;
 *  - single-label hosts (http://valheim:9090) — mDNS or /etc/hosts aliases
 *    that other machines may not resolve.
 */
export function detectPublicUrl(): string {
  const { hostname, origin } = window.location
  const host = (hostname || '').toLowerCase()
  if (!host) return ''
  if (host === 'localhost' || host.endsWith('.localhost') || host === 'host.docker.internal') return ''
  // IPv4 literal
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host)) return ''
  // IPv6 literal (location.hostname keeps the brackets)
  if (host.startsWith('[') || host.includes(':')) return ''
  // single-label LAN alias
  if (!host.includes('.')) return ''
  return origin
}
