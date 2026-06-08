import { toast } from 'sonner';
import {
  MoreHorizontal,
  RotateCcw,
  Square,
  Trash2,
  Play,
  ArrowUpCircle,
  ExternalLink,
} from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { getContainerStateColor } from '@/shared/lib/utils';
import type { DockerContainer, DockerPort } from './schemas';

const fmtBytes = (b: number) =>
  b >= 1e9 ? `${(b / 1e9).toFixed(1)}G` : b >= 1e6 ? `${(b / 1e6).toFixed(1)}M` : b >= 1e3 ? `${(b / 1e3).toFixed(0)}K` : `${b}B`;

function fmtUptime(created: string) {
  const ms = Date.now() - new Date(created).getTime();
  if (isNaN(ms) || ms < 0) return '';
  const d = Math.floor(ms / 86400000);
  const h = Math.floor((ms % 86400000) / 3600000);
  const m = Math.floor((ms % 3600000) / 60000);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function fmtPorts(ports: DockerPort[]): string {
  if (!ports?.length) return '';
  return ports
    .filter((p) => p.public_port)
    .map((p) => `:${p.public_port}→${p.private_port}`)
    .slice(0, 4)
    .join(' ');
}

/** Strip the scheme from a URL for a compact chip label. */
function hostFromURL(u: string): string {
  return u.replace(/^https?:\/\//, '').replace(/\/$/, '');
}

function metricColor(pct: number) {
  if (pct >= 90) return '#ef4444';
  if (pct >= 75) return '#f59e0b';
  return '#6b7280';
}

function shortDigest(d?: string) {
  return (d || '').replace(/^sha256:/, '').slice(0, 12);
}

/** Small circular usage gauge. */
export function RingGauge({ value, color, size = 20 }: { value: number; color: string; size?: number }) {
  const cx = size / 2;
  const r = (size - 5) / 2;
  const circ = 2 * Math.PI * r;
  const dash = Math.min(value / 100, 1) * circ;
  return (
    <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }} className="shrink-0">
      <circle cx={cx} cy={cx} r={r} fill="none" stroke="currentColor" strokeWidth={2.5} className="text-muted" />
      <circle
        cx={cx}
        cy={cx}
        r={r}
        fill="none"
        stroke={color}
        strokeWidth={2.5}
        strokeDasharray={`${dash} ${circ}`}
        strokeLinecap="round"
      />
    </svg>
  );
}

const notImplemented = (action: string, name: string) =>
  toast.info(`${action} — not implemented`, {
    description: `Container actions for "${name}" are not yet available.`,
    duration: 3000,
  });

function ContainerActions({ name, state }: { name: string; state: string }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="shrink-0 rounded p-0.5 text-muted-foreground/60 transition-colors hover:bg-muted hover:text-foreground">
        <MoreHorizontal className="h-3.5 w-3.5" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-36 text-xs">
        {state !== 'running' && (
          <DropdownMenuItem className="gap-2 text-xs" onClick={() => notImplemented('Start', name)}>
            <Play className="h-3 w-3" /> Start
          </DropdownMenuItem>
        )}
        <DropdownMenuItem className="gap-2 text-xs" onClick={() => notImplemented('Restart', name)}>
          <RotateCcw className="h-3 w-3" /> Restart
        </DropdownMenuItem>
        {state === 'running' && (
          <DropdownMenuItem className="gap-2 text-xs" onClick={() => notImplemented('Stop', name)}>
            <Square className="h-3 w-3" /> Stop
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="gap-2 text-xs text-destructive focus:text-destructive"
          onClick={() => notImplemented('Remove', name)}
        >
          <Trash2 className="h-3 w-3" /> Remove
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * One container row — shared between the per-node Docker widget and the
 * application detail page. Shows live CPU/mem gauges, network, image + tag,
 * ports, uptime, disk size, and the registry update status.
 */
export function ContainerRow({ container: c }: { container: DockerContainer }) {
  const cpu = c.stats.cpu_percent ?? 0;
  const mem = c.stats.memory_percent ?? 0;
  const cpuColor = metricColor(cpu);
  const memColor = metricColor(mem);
  const ports = fmtPorts(c.ports);
  const routed = (c.ports ?? []).filter((p) => p.public_url);
  const uptime = c.state === 'running' ? fmtUptime(c.created) : '';

  return (
    <div className="space-y-0.5 px-2.5 py-1.5">
      {/* Main row */}
      <div className="flex min-w-0 items-center gap-2 text-xs">
        <span className="h-1.5 w-1.5 shrink-0 rounded-full" style={{ backgroundColor: getContainerStateColor(c.state) }} />
        <span className="min-w-0 flex-1 truncate font-medium">{c.name}</span>

        {c.update_available && (
          <span
            className="inline-flex shrink-0 items-center gap-0.5 rounded bg-amber-500/15 px-1 py-0 text-[9px] font-medium text-amber-600 dark:text-amber-400"
            title={`Newer image in registry. Running ${c.image_version || shortDigest(c.local_digest) || '?'} (${shortDigest(c.local_digest) || '?'}) → registry now ${shortDigest(c.remote_digest) || '?'}`}
          >
            <ArrowUpCircle className="h-2.5 w-2.5" /> update
          </span>
        )}

        <div className="flex shrink-0 items-center gap-1">
          <RingGauge value={cpu} color={cpuColor} />
          <span className="w-7 text-right tabular-nums" style={{ color: cpuColor }}>
            {cpu.toFixed(0)}%
          </span>
          <span className="text-[10px] text-muted-foreground/50">cpu</span>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <RingGauge value={mem} color={memColor} />
          <span className="w-7 text-right tabular-nums" style={{ color: memColor }}>
            {mem.toFixed(0)}%
          </span>
          <span className="text-[10px] text-muted-foreground/50">mem</span>
        </div>

        <span className="hidden shrink-0 tabular-nums text-[10px] text-muted-foreground sm:inline">
          ↓{fmtBytes(c.stats.network_rx ?? 0)} ↑{fmtBytes(c.stats.network_tx ?? 0)}
        </span>

        <ContainerActions name={c.name} state={c.state} />
      </div>

      {/* Sub-info row */}
      <div className="flex min-w-0 items-center gap-2 pl-3.5 text-[10px] text-muted-foreground/50">
        <span className="truncate font-mono" title={c.image}>
          {c.image}
          {c.image_version ? <span className="text-muted-foreground/40"> · v{c.image_version}</span> : null}
        </span>
        {ports && <span className="shrink-0 font-mono">{ports}</span>}
        {c.size_root_fs ? <span className="shrink-0">{fmtBytes(c.size_root_fs)}</span> : null}
        {uptime && <span className="shrink-0">{uptime}</span>}
      </div>

      {/* Reverse-proxy (Traefik) public URLs — including ports the container
          doesn't publish to the host. */}
      {routed.length > 0 && (
        <div className="flex flex-wrap items-center gap-1 pl-3.5">
          {routed.map((p, i) => (
            <a
              key={`${p.private_port}-${i}`}
              href={p.public_url}
              target="_blank"
              rel="noreferrer"
              onClick={(e) => e.stopPropagation()}
              title={`${p.public_url} → :${p.private_port}`}
              className="inline-flex items-center gap-1 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary transition-colors hover:bg-primary/20"
            >
              <ExternalLink className="h-2.5 w-2.5" />
              {hostFromURL(p.public_url as string)}
              {p.private_port ? <span className="text-primary/60">:{p.private_port}</span> : null}
            </a>
          ))}
        </div>
      )}

      {/* Update resolution: human-readable running version → registry version */}
      {c.update_available && (
        <div className="pl-3.5 text-[10px] text-amber-600/80 dark:text-amber-400/70">
          <span className="font-medium">↑ update</span>
          <span className="text-amber-600/60 dark:text-amber-400/50"> — running </span>
          <span className="font-mono" title={c.local_digest}>
            {c.image_version || shortDigest(c.local_digest) || '?'}
          </span>
          <span className="text-amber-600/60 dark:text-amber-400/50"> → registry </span>
          <span className="font-mono" title={c.remote_digest}>
            {c.remote_version
              ? `${c.remote_version}${c.remote_version === c.image_version ? ' (rebuilt)' : ''}`
              : `newer build (${shortDigest(c.remote_digest) || '?'})`}
          </span>
        </div>
      )}
    </div>
  );
}
