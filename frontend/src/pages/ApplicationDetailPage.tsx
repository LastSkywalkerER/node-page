import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { useMetricsStream } from '@/shared/hooks/useEventSource';
import { useLiveMetricsQuerySync } from '@/shared/hooks/useLiveMetricsQuerySync';
import { Boxes, Cpu, MemoryStick, Network, HardDrive, Database, FolderOpen, ExternalLink, ArrowUpCircle } from 'lucide-react';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton';
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty';
import { AppIcon } from '@/shared/ui/AppIcon';
import { ContainerRow, RingGauge } from '@/widgets/docker/ContainerRow';
import { LogViewer } from '@/widgets/applications/LogViewer';
import { ComposeYaml } from '@/widgets/applications/ComposeYaml';
import { cn } from '@/lib/utils';
import { useHosts } from '@/widgets/hosts/useHosts';
import {
  useApplicationDetail,
  useApplicationCompose,
  useApplicationLogs,
} from '@/widgets/applications/useApplicationDetail';
import type { DockerContainer } from '@/widgets/docker/schemas';

function metricColor(pct: number): string {
  if (pct >= 90) return '#ef4444';
  if (pct >= 75) return '#f59e0b';
  return '#10b981';
}

function fmtBytes(b: number): string {
  return b >= 1e9
    ? `${(b / 1e9).toFixed(1)} GB`
    : b >= 1e6
      ? `${(b / 1e6).toFixed(1)} MB`
      : b >= 1e3
        ? `${(b / 1e3).toFixed(0)} KB`
        : `${b} B`;
}

function StatChip({ icon: Icon, label, value, color, pct }: { icon: typeof Cpu; label: string; value: string; color?: string; pct?: number }) {
  return (
    <div className="flex items-center gap-2 rounded-xl border border-border/60 bg-card backdrop-blur-xl backdrop-saturate-150 px-3 py-2 dark:border-white/10">
      {pct != null ? (
        <RingGauge value={pct} color={color ?? '#10b981'} size={26} />
      ) : (
        <Icon className="h-4 w-4 text-muted-foreground" />
      )}
      <div className="min-w-0">
        <p className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</p>
        <p className="font-mono text-sm font-medium tabular-nums" style={color ? { color } : undefined}>{value}</p>
      </div>
    </div>
  );
}

export function ApplicationDetailPage() {
  const { hostId, project } = useParams<{ hostId: string; project: string }>();
  const hid = Number(hostId);
  const proj = decodeURIComponent(project ?? '');

  // Live SSE updates for the local host: keeps the per-container + aggregate
  // stats in lockstep with the 5s collection cycle (synced into the detail cache).
  useMetricsStream(hid);
  useLiveMetricsQuerySync(hid);

  const { data, isLoading } = useApplicationDetail(hid, proj);
  const { data: hostsData } = useHosts();
  const host = hostsData?.hosts?.find((h) => h.id === hid);
  const app = data?.application;

  const [tab, setTab] = useState('resources');
  const [selectedContainer, setSelectedContainer] = useState<string>('');
  const [showReal, setShowReal] = useState(false);

  const containers: DockerContainer[] = app?.containers ?? [];
  const activeContainer = selectedContainer || containers.find((c) => c.state === 'running')?.id || containers[0]?.id || '';

  const compose = useApplicationCompose(hid, proj, tab === 'compose');
  const logs = useApplicationLogs(hid, proj, activeContainer, { enabled: tab === 'logs' && !!activeContainer });


  if (isLoading && !app) return <div className="mx-auto max-w-5xl px-4 py-6"><MetricCardSkeleton /></div>;
  if (!app) {
    return (
      <div className="mx-auto max-w-5xl px-4 py-6">
        <MetricWidgetEmpty icon={Boxes} label="Application not found" />
      </div>
    );
  }

  const cpu = app.stats.cpu_percent ?? 0;

  // All externally-reachable URLs: the primary public link + every reverse-proxy
  // routed port URL, de-duplicated. Rendered as individual "open" links.
  const stripScheme = (u: string) => u.replace(/^https?:\/\//, '').replace(/\/$/, '');
  const urls = Array.from(
    new Set([
      ...(app.public_url ? [app.public_url] : []),
      ...((app.ports ?? []).map((p) => p.public_url).filter(Boolean) as string[]),
    ])
  );

  return (
    <div className="mx-auto max-w-5xl px-4 py-6">
      {/* Header */}
      <div className="mb-5 flex items-start gap-4">
        <AppIcon slug={app.icon_slug} altSlug={app.project} publicUrl={app.public_url} name={app.display_name} className="h-12 w-12 shrink-0 rounded-lg" />
        <div className="min-w-0 flex-1">
          <h1 className="truncate font-display text-2xl font-semibold tracking-wide">{app.display_name}</h1>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span className="font-mono">{host ? (host.display_name || host.name) : `host #${hid}`}</span>
            <Badge variant="secondary" className="h-4 px-1.5 py-0 font-mono text-[10px]">
              {app.running_containers}/{app.total_containers} containers
            </Badge>
            {app.service_count > 1 && <span>{app.service_count} services</span>}
            {app.is_singleton && <span className="text-muted-foreground/60">standalone</span>}
            {app.updates_available > 0 && (
              <span className="inline-flex items-center gap-0.5 rounded bg-amber-500/15 px-1.5 py-0 text-[10px] font-medium text-amber-600 dark:text-amber-400">
                <ArrowUpCircle className="h-3 w-3" /> {app.updates_available} update{app.updates_available > 1 ? 's' : ''}
              </span>
            )}
          </div>
        </div>
        {urls.length > 0 && (
          <div className="flex max-w-[55%] shrink-0 flex-wrap justify-end gap-1.5">
            {urls.map((u) => (
              <a
                key={u}
                href={u}
                target="_blank"
                rel="noreferrer"
                title={u}
                className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-primary/10 px-3 py-1.5 text-xs text-primary transition-colors hover:bg-primary/20"
              >
                <ExternalLink className="h-3.5 w-3.5 shrink-0" />
                <span className="truncate">{stripScheme(u)}</span>
              </a>
            ))}
          </div>
        )}
      </div>

      {/* Summary chips */}
      <div className="mb-6 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
        <StatChip icon={Cpu} label="CPU total" value={`${cpu.toFixed(1)}%`} color={metricColor(cpu)} pct={cpu} />
        <StatChip
          icon={MemoryStick}
          label="Memory"
          value={fmtBytes(app.stats.memory_usage ?? 0)}
          color={metricColor(app.stats.memory_percent ?? 0)}
          pct={app.stats.memory_percent ?? 0}
        />
        <StatChip icon={Database} label="Disk" value={fmtBytes(app.total_size_root_fs ?? 0)} />
        <StatChip icon={Network} label="Net ↓/↑" value={`${fmtBytes(app.stats.network_rx ?? 0)} / ${fmtBytes(app.stats.network_tx ?? 0)}`} />
        <StatChip icon={HardDrive} label="Block r/w" value={`${fmtBytes(app.stats.block_read ?? 0)} / ${fmtBytes(app.stats.block_write ?? 0)}`} />
      </div>

      {/* Section switcher — same segmented style as the header nav controls */}
      <nav
        className={cn(
          'mb-4 inline-flex shrink-0 gap-0.5 overflow-hidden rounded-lg border p-0.5',
          'border-border/70 bg-muted/35 backdrop-blur-md',
          'dark:border-cyan-500/15 dark:bg-black/35 dark:shadow-[0_0_24px_-8px_oklch(0.72_0.16_195/0.2)]'
        )}
        aria-label="Application sections"
      >
        {([
          ['resources', 'Resources'],
          ['logs', 'Logs'],
          ['compose', 'Composition'],
        ] as const).map(([key, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={cn(
              'cursor-pointer select-none rounded-md px-3 py-1 text-xs font-medium transition-all duration-200',
              tab === key
                ? 'bg-primary text-primary-foreground shadow-[0_0_20px_-4px_oklch(0.72_0.16_195/0.45)]'
                : 'text-muted-foreground hover:bg-background/50 hover:text-foreground dark:hover:bg-white/5'
            )}
          >
            {label}
          </button>
        ))}
      </nav>

      {/* Resources */}
      {tab === 'resources' && (
        <div className="space-y-4">
        <Card>
            <CardHeader className="pb-2">
              <span className="text-sm font-medium text-muted-foreground">Containers</span>
            </CardHeader>
            <CardContent className="divide-y divide-border/60 pt-0">
              {containers.map((c) => (
                <ContainerRow key={c.id} container={c} />
              ))}
            </CardContent>
          </Card>

          {/* Volumes & paths — grouped by container */}
          <Card>
            <CardHeader className="pb-2">
              <span className="text-sm font-medium text-muted-foreground">Volumes &amp; paths</span>
            </CardHeader>
            <CardContent className="pt-0">
              {containers.every((c) => !(c.mounts ?? []).some((m) => m.type !== 'tmpfs' && m.source)) ? (
                <p className="py-2 text-xs text-muted-foreground">No named volumes or bind mounts.</p>
              ) : (
                <div className="space-y-3">
                  {containers.map((c) => {
                    const vols = (c.mounts ?? []).filter((m) => m.type !== 'tmpfs' && m.source);
                    if (vols.length === 0) return null;
                    return (
                      <div key={c.id}>
                        <div className="mb-1 flex items-center gap-1.5">
                          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-muted-foreground/40" />
                          <span className="truncate text-xs font-medium">{c.name}</span>
                          {c.service ? <span className="text-[10px] text-muted-foreground/50">{c.service}</span> : null}
                        </div>
                        <div className="space-y-1.5 pl-3.5">
                          {vols.map((v, i) => {
                            const anon = v.name && /^[0-9a-f]{64}$/.test(v.name);
                            const label = anon ? 'anonymous volume' : v.name || v.type;
                            return (
                              <div key={`${v.source}:${v.destination}:${i}`} className="flex items-start gap-2 text-xs">
                                {v.type === 'bind' ? (
                                  <FolderOpen className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                                ) : (
                                  <Database className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                                )}
                                <div className="min-w-0 flex-1">
                                  <div className="flex items-center gap-1.5">
                                    <span className="truncate font-medium">{label}</span>
                                    <Badge variant="secondary" className="h-4 px-1 py-0 text-[9px]">
                                      {v.rw ? 'rw' : 'ro'}
                                    </Badge>
                                    {v.size ? (
                                      <span className="shrink-0 font-mono text-[9px] tabular-nums text-muted-foreground/80">
                                        {fmtBytes(v.size)}
                                      </span>
                                    ) : null}
                                  </div>
                                  <p
                                    className="truncate font-mono text-[10px] text-muted-foreground/70"
                                    title={`${v.source} → ${v.destination}`}
                                  >
                                    {v.source} <span className="text-muted-foreground/40">→</span> {v.destination}
                                  </p>
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* Logs */}
      {tab === 'logs' && (
        <Card>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium text-muted-foreground">Logs</span>
                <select
                  value={activeContainer}
                  onChange={(e) => setSelectedContainer(e.target.value)}
                  className="max-w-[60%] truncate rounded-md border border-border/60 bg-background px-2 py-1 text-xs dark:border-white/10"
                >
                  {containers.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </div>
            </CardHeader>
            <CardContent className="pt-0">
              {logs.data && logs.data.available === false ? (
                <p className="py-6 text-center text-xs text-muted-foreground">
                  {logs.data.reason ?? 'Logs are not available for this host.'}
                </p>
              ) : (
                <LogViewer logs={logs.data?.logs ?? ''} loading={logs.isLoading} />
              )}
            </CardContent>
          </Card>
      )}

      {/* Composition */}
      {tab === 'compose' && (
        <Card>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium text-muted-foreground">Composition</span>
                {compose.data?.real_available && (
                  <div className="flex rounded-md border border-border/60 p-0.5 text-[11px] dark:border-white/10">
                    <button
                      onClick={() => setShowReal(false)}
                      className={`rounded px-2 py-0.5 ${!showReal ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}
                    >
                      Reconstructed
                    </button>
                    <button
                      onClick={() => setShowReal(true)}
                      className={`rounded px-2 py-0.5 ${showReal ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}
                    >
                      Real YAML
                    </button>
                  </div>
                )}
              </div>
            </CardHeader>
            <CardContent className="pt-0">
              {compose.isLoading ? (
                <p className="py-6 text-center text-xs text-muted-foreground">Loading…</p>
              ) : (
                <>
                  {!compose.data?.real_available && (
                    <p className="mb-2 text-[11px] text-muted-foreground/70">
                      Reconstructed from container metadata (read-only). The original compose file is not reachable from this server.
                    </p>
                  )}
                  <ComposeYaml
                    content={
                      (showReal && compose.data?.real_available ? compose.data?.real : compose.data?.synthetic) ?? ''
                    }
                  />
                  {showReal && compose.data?.config_files && compose.data.config_files.length > 0 && (
                    <p className="mt-2 font-mono text-[10px] text-muted-foreground/60">
                      {compose.data.config_files.join(', ')}
                    </p>
                  )}
                </>
              )}
            </CardContent>
          </Card>
      )}
    </div>
  );
}
