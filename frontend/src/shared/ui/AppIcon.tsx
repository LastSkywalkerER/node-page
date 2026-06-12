import { useEffect, useState } from 'react';
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

/**
 * Renders an application's icon from CDN candidates, cascading through fallbacks
 * on load error and ending at a generic placeholder.
 */
export function AppIcon({ slug, altSlug, publicUrl, name, className }: AppIconProps) {
  const candidates = iconCandidates(slug, publicUrl, altSlug);
  const [idx, setIdx] = useState(0);

  // Reset the candidate index whenever the icon inputs change.
  useEffect(() => {
    setIdx(0);
  }, [slug, altSlug, publicUrl]);

  const src = candidates[idx];

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
      onError={() => setIdx((i) => i + 1)}
      className={cn('object-contain', className)}
    />
  );
}
