import { useNavigate } from 'react-router-dom';
import { ExternalLink, Box, ArrowUpCircle } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { AppIcon } from '@/shared/ui/AppIcon';
import { cn } from '@/lib/utils';
import type { DockerApplication } from './schemas';

function metricColor(pct: number): string {
  if (pct >= 90) return '#ef4444';
  if (pct >= 75) return '#f59e0b';
  return '#10b981';
}

function fmtBytes(b: number): string {
  return b >= 1e9
    ? `${(b / 1e9).toFixed(1)}G`
    : b >= 1e6
      ? `${(b / 1e6).toFixed(1)}M`
      : b >= 1e3
        ? `${(b / 1e3).toFixed(0)}K`
        : `${b}B`;
}

interface ApplicationTileProps {
  app: DockerApplication;
  hostId: number;
  hostIPv4?: string;
}

export function ApplicationTile({ app, hostId, hostIPv4 }: ApplicationTileProps) {
  const navigate = useNavigate();
  const cpu = app.stats.cpu_percent ?? 0;
  const mem = app.stats.memory_percent ?? 0;
  const ports = (app.ports ?? []).filter((p) => p.public_port).slice(0, 6);
  const internalLinks =
    hostIPv4 && ports.length > 0
      ? ports.map((p) => ({ port: p.public_port as number, href: `http://${hostIPv4}:${p.public_port}` }))
      : [];

  const stop = (e: React.MouseEvent) => e.stopPropagation();
  const goDetail = () => navigate(`/applications/${hostId}/${encodeURIComponent(app.project)}`);

  return (
    <div
      role="link"
      tabIndex={0}
      onClick={goDetail}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          goDetail();
        }
      }}
      className={cn(
        'group flex h-full cursor-pointer flex-col rounded-xl border p-4 transition-all duration-300 ease-out',
        'border-border/60 bg-card backdrop-blur-xl dark:border-white/10',
        'hover:-translate-y-0.5 hover:shadow-md hover:shadow-black/8',
        'dark:hover:shadow-[0_0_56px_-14px_oklch(0.72_0.22_320/0.22)]',
        'outline-none focus-visible:ring-2 focus-visible:ring-ring'
      )}
    >
      <div className="flex items-start gap-3">
        <AppIcon
          slug={app.icon_slug}
          name={app.display_name}
          className="h-10 w-10 shrink-0 rounded-md"
        />
        <div className="min-w-0 flex-1">
          <h3 className="truncate font-display text-sm font-semibold tracking-wide transition-colors group-hover:text-primary">
            {app.display_name}
          </h3>
          <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
            <Badge variant="secondary" className="h-4 px-1.5 py-0 font-mono text-[10px]">
              {app.running_containers}/{app.total_containers} ctr
            </Badge>
            {app.service_count > 1 && (
              <span className="text-[10px] text-muted-foreground">{app.service_count} svc</span>
            )}
            {app.is_singleton && (
              <span className="inline-flex items-center gap-0.5 text-[10px] text-muted-foreground/60">
                <Box className="h-2.5 w-2.5" /> standalone
              </span>
            )}
            {app.updates_available > 0 && (
              <span
                className="inline-flex items-center gap-0.5 rounded bg-amber-500/15 px-1 py-0 text-[10px] font-medium text-amber-600 dark:text-amber-400"
                title={`${app.updates_available} container(s) have a newer image available`}
              >
                <ArrowUpCircle className="h-2.5 w-2.5" /> {app.updates_available}
              </span>
            )}
          </div>
        </div>
      </div>

      <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
        <div className="flex flex-col gap-0.5 rounded-md bg-muted/30 px-2 py-1">
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">CPU</span>
          <span className="font-mono font-medium tabular-nums" style={{ color: metricColor(cpu) }}>
            {cpu.toFixed(0)}%
          </span>
        </div>
        <div className="flex flex-col gap-0.5 rounded-md bg-muted/30 px-2 py-1">
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">MEM</span>
          <span className="font-mono font-medium tabular-nums" style={{ color: metricColor(mem) }}>
            {fmtBytes(app.stats.memory_usage ?? 0)}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 rounded-md bg-muted/30 px-2 py-1">
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">DISK</span>
          <span className="font-mono font-medium tabular-nums text-foreground/80">
            {fmtBytes(app.total_size_root_fs ?? 0)}
          </span>
        </div>
      </div>

      {(ports.length > 0 || app.public_url) && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          {app.public_url && (
            <a
              href={app.public_url}
              target="_blank"
              rel="noreferrer"
              onClick={stop}
              className="inline-flex items-center gap-1 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary transition-colors hover:bg-primary/20"
            >
              <ExternalLink className="h-2.5 w-2.5" /> open
            </a>
          )}
          {internalLinks.length > 0
            ? internalLinks.map((l) => (
                <a
                  key={l.port}
                  href={l.href}
                  target="_blank"
                  rel="noreferrer"
                  onClick={stop}
                  className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground transition-colors hover:text-foreground"
                >
                  :{l.port}
                </a>
              ))
            : ports.map((p) => (
                <span
                  key={p.public_port}
                  className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground"
                >
                  :{p.public_port}
                </span>
              ))}
        </div>
      )}
    </div>
  );
}
