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
 * Build candidate icon URLs from the resolved slug (or override). Supports:
 * - full URLs / local paths → used as-is
 * - Homepage-style prefixes: `sh-` (selfh.st), `si-` (Simple Icons)
 * - bare slug → dashboard-icons, then selfh.st (svg → webp/png)
 */
export function iconCandidates(raw: string | undefined | null): string[] {
  const v = (raw ?? '').trim();
  if (!v) return [];

  if (v.startsWith('http://') || v.startsWith('https://') || v.startsWith('/')) {
    return [v];
  }

  if (v.startsWith('sh-')) {
    const s = normalizeSlug(v.slice(3));
    return [`${SELFHST}/svg/${s}.svg`, `${SELFHST}/webp/${s}.webp`, `${SELFHST}/png/${s}.png`];
  }
  if (v.startsWith('si-')) {
    const s = normalizeSlug(v.slice(3));
    return [`${SIMPLE_ICONS}/${s}.svg`];
  }

  const s = normalizeSlug(v);
  if (!s) return [];
  return [
    `${DASHBOARD_ICONS}/svg/${s}.svg`,
    `${SELFHST}/svg/${s}.svg`,
    `${DASHBOARD_ICONS}/webp/${s}.webp`,
    `${SELFHST}/png/${s}.png`,
  ];
}
