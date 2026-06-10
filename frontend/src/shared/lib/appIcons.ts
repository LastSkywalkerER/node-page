// Resolve an application's icon slug into an ordered list of candidate URLs.
// The <AppIcon> component tries them in order via <img onError> until one loads,
// then falls back to a generic placeholder. No network probing here.

const DASHBOARD_ICONS = 'https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons';
const SELFHST = 'https://cdn.jsdelivr.net/gh/selfhst/icons';
const SIMPLE_ICONS = 'https://cdn.jsdelivr.net/gh/simple-icons/simple-icons/icons';

function normalizeSlug(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/\.(svg|png|webp|ico)$/i, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

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
 * - Homepage-style prefixes: `sh-` (selfh.st), `si-` (Simple Icons)
 * - bare slug → dashboard-icons, then selfh.st (svg → webp/png)
 *
 * When `publicUrl` is given, the app's favicon is appended as a final fallback,
 * so a self-hosted app with no registry-icon match still shows its own icon.
 */
export function iconCandidates(raw: string | undefined | null, publicUrl?: string | null): string[] {
  const v = (raw ?? '').trim();
  const fav = faviconCandidates(publicUrl);

  if (!v) return fav;

  if (v.startsWith('http://') || v.startsWith('https://') || v.startsWith('/')) {
    return [v, ...fav];
  }

  if (v.startsWith('sh-')) {
    const s = normalizeSlug(v.slice(3));
    return [`${SELFHST}/svg/${s}.svg`, `${SELFHST}/webp/${s}.webp`, `${SELFHST}/png/${s}.png`, ...fav];
  }
  if (v.startsWith('si-')) {
    const s = normalizeSlug(v.slice(3));
    return [`${SIMPLE_ICONS}/${s}.svg`, ...fav];
  }

  const s = normalizeSlug(v);
  if (!s) return fav;
  return [
    `${DASHBOARD_ICONS}/svg/${s}.svg`,
    `${SELFHST}/svg/${s}.svg`,
    `${DASHBOARD_ICONS}/webp/${s}.webp`,
    `${SELFHST}/png/${s}.png`,
    ...fav,
  ];
}
