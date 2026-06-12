// Resolve an application's icon slug into an ordered list of candidate URLs.
// The <AppIcon> component tries them in order via <img onError> until one loads,
// then falls back to a generic placeholder. No network probing here.

/**
 * Last-resort favicon candidates derived from an application's public URL — used
 * when the image-derived slug matches no icon CDN (e.g. a locally-built app). The
 * site's own /favicon.ico is tried first (works for reachable self-hosted hosts),
 * then DuckDuckGo's favicon proxy as a fallback for public domains.
 */
function faviconCandidates(publicUrl: string | undefined | null): string[] {
  const u = (publicUrl ?? '').trim();
  if (!u) return [];
  try {
    const parsed = new URL(u);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return [];
    return [
      `${parsed.origin}/favicon.ico`,
      `https://icons.duckduckgo.com/ip3/${parsed.hostname}.ico`,
    ];
  } catch {
    return [];
  }
}

/**
 * Build candidate icon URLs from the resolved slug (or override). Supports:
 * - full URLs / local paths → used as-is
 * - anything else → the backend resolver (`/api/v1/app-icons/:slug`), which
 *   handles `sh-`/`si-` prefixes, matches collapsed names against the live
 *   selfh.st icon index ("nginxproxymanager" → "nginx-proxy-manager"), tries
 *   the CDN registries server-side and caches the bytes for every client.
 *
 * When `publicUrl` is given, the app's favicon is appended as a final fallback,
 * so a self-hosted app with no registry-icon match still shows its own icon.
 */
export function iconCandidates(
  raw: string | undefined | null,
  publicUrl?: string | null,
  altSlug?: string | null
): string[] {
  const v = (raw ?? '').trim();
  const alt = (altSlug ?? '').trim();
  const fav = faviconCandidates(publicUrl);
  // Compose project names ("netbird", "crafty") often resolve when the
  // image-derived slug ("dashboard", "crafty-4") does not — try both.
  const altCandidates =
    alt && alt.toLowerCase() !== v.toLowerCase()
      ? [`/api/v1/app-icons/${encodeURIComponent(alt)}`]
      : [];

  if (!v) return [...altCandidates, ...fav];

  if (v.startsWith('http://') || v.startsWith('https://') || v.startsWith('/')) {
    return [v, ...altCandidates, ...fav];
  }

  return [`/api/v1/app-icons/${encodeURIComponent(v)}`, ...altCandidates, ...fav];
}
