import { useEffect, useMemo, useState } from 'react';
import { Boxes } from 'lucide-react';
import { iconCandidates } from '@/shared/lib/appIcons';
import { cn } from '@/lib/utils';

interface AppIconProps {
  slug?: string | null;
  /** Alternative slug (e.g. the compose project name) tried after `slug`. */
  altSlug?: string | null;
  /** App public URL — its favicon is used as a final fallback when no icon matches. */
  publicUrl?: string | null;
  name?: string;
  className?: string;
}

// Per-tab memory of which candidate URLs loaded and which 404'd. Tiles remount
// on every list ↔ detail navigation and re-render on every 5s/30s refetch; without
// this each of them restarted the whole <img onError> cascade from candidate 0
// (two 404s, then the favicon), so an icon that had already loaded blinked out
// and back — or stayed gone whenever the browser cache was disabled. Strings
// only, a few dozen entries at most.
const loadedSrc = new Set<string>();
const failedSrc = new Set<string>();

/** First candidate that has not already been seen to fail (a known-good one wins). */
function pickStart(candidates: string[]): number {
  const known = candidates.findIndex((c) => loadedSrc.has(c));
  if (known >= 0) return known;
  const i = candidates.findIndex((c) => !failedSrc.has(c));
  return i >= 0 ? i : candidates.length;
}

/**
 * Renders an application's icon from CDN candidates, cascading through fallbacks
 * on load error and ending at a generic placeholder.
 */
export function AppIcon({ slug, altSlug, publicUrl, name, className }: AppIconProps) {
  const candidates = useMemo(() => iconCandidates(slug, publicUrl, altSlug), [slug, publicUrl, altSlug]);
  const key = candidates.join('\n');
  const [state, setState] = useState(() => ({ key, idx: pickStart(candidates) }));

  // Re-pick only when the candidate LIST changes (not on every render), and
  // even then skip straight past candidates already known to fail.
  useEffect(() => {
    setState((s) => (s.key === key ? s : { key, idx: pickStart(candidates) }));
  }, [key, candidates]);

  const src = state.key === key ? candidates[state.idx] : candidates[pickStart(candidates)];

  if (!src) {
    return (
      <span
        className={cn(
          'inline-flex items-center justify-center bg-muted/50 text-muted-foreground',
          className
        )}
      >
        <Boxes className="h-1/2 w-1/2" />
      </span>
    );
  }

  return (
    <img
      src={src}
      alt={name ?? slug ?? 'application'}
      loading="lazy"
      onLoad={() => loadedSrc.add(src)}
      onError={() => {
        failedSrc.add(src);
        setState((s) => {
          let next = s.idx + 1;
          while (next < candidates.length && failedSrc.has(candidates[next])) next++;
          return { key, idx: next };
        });
      }}
      className={cn('object-contain', className)}
    />
  );
}
