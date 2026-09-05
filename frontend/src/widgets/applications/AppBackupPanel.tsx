import { useMemo, useState } from 'react';
import {
  Archive,
  ArrowDownCircle,
  ArrowUpCircle,
  ExternalLink,
  History,
  Loader2,
  RotateCcw,
  ShieldAlert,
  Trash2,
} from 'lucide-react';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { confirmDialog } from '@/shared/lib/confirmDialog';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import {
  classifyMove,
  useAppBackupPlan,
  useAppBackupRuns,
  useAppBackupStatus,
  useAppSnapshots,
  useBackupApp,
  useDeleteSnapshot,
  useImageVersions,
  useRestoreApp,
  useUpdateApp,
  type AppSnapshot,
  type MoveSeverity,
  type ServiceState,
  type ServiceTarget,
} from './useAppBackup';

function fmtBytes(b?: number): string {
  if (!b) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = b;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return isNaN(d.getTime()) ? '—' : d.toLocaleString();
}

/** Drops the registry/namespace so a move reads as "1.27-alpine → 1.31-alpine". */
function shortRef(ref: string): string {
  const at = ref.lastIndexOf('@');
  const body = at > 0 ? ref.slice(0, at) : ref;
  const colon = body.lastIndexOf(':');
  if (colon > body.lastIndexOf('/')) return body.slice(colon + 1);
  return body.slice(body.lastIndexOf('/') + 1);
}

function apiError(e: unknown): string {
  const err = e as { response?: { data?: { error?: string } }; message?: string };
  return err?.response?.data?.error ?? err?.message ?? 'Request failed';
}

/** Per-service version picker row. */
function ServiceRow({
  svc,
  target,
  onChange,
}: {
  svc: ServiceState;
  target: string;
  onChange: (tag: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const { data, isFetching } = useImageVersions(svc.image, open);
  const tags = data?.tags ?? [];
  const severity = classifyMove(svc.tag, target);

  return (
    <div className="flex flex-wrap items-center gap-2 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{svc.service}</span>
          {svc.update_available && (
            <Badge variant="secondary" className="gap-1 text-[10px]">
              <ArrowUpCircle className="h-3 w-3" /> update
            </Badge>
          )}
        </div>
        <div className="truncate font-mono text-[11px] text-muted-foreground">{svc.repo}</div>
      </div>

      <Badge variant="outline" className="font-mono text-[11px]">
        {svc.tag || '—'}
      </Badge>
      <span className="text-muted-foreground">→</span>

      <select
        className="h-8 rounded-md border border-input bg-background px-2 font-mono text-xs"
        value={target}
        onFocus={() => setOpen(true)}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value={svc.tag}>{svc.tag || '(unchanged)'}</option>
        {tags
          .filter((t) => t !== svc.tag)
          .map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
      </select>
      {isFetching && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
      <SeverityBadge severity={severity} />
    </div>
  );
}

function SeverityBadge({ severity }: { severity: MoveSeverity }) {
  if (severity === 'same') return null;
  const map: Record<Exclude<MoveSeverity, 'same'>, { label: string; cls: string }> = {
    downgrade: { label: 'downgrade', cls: 'bg-destructive/15 text-destructive border-destructive/40' },
    major: { label: 'major', cls: 'bg-destructive/15 text-destructive border-destructive/40' },
    minor: { label: 'minor', cls: 'bg-amber-500/15 text-amber-600 border-amber-500/40' },
    unknown: { label: 'unversioned', cls: 'bg-muted text-muted-foreground' },
  };
  const s = map[severity];
  return (
    <Badge variant="outline" className={cn('text-[10px]', s.cls)}>
      {s.label}
    </Badge>
  );
}

/**
 * Backup, image update and restore for one application.
 *
 * Actions are never hidden when unavailable: the panel shows the reason,
 * because an operator who cannot back up needs to know why, not to wonder
 * where the button went.
 */
export function AppBackupPanel({
  hostId,
  project,
  dashboardUrl,
}: {
  hostId: number;
  project: string;
  dashboardUrl?: string;
}) {
  const { data: status } = useAppBackupStatus();
  const { data: plan, isLoading } = useAppBackupPlan(hostId, project);
  const { data: snaps } = useAppSnapshots(project, !!plan && !plan.blocked);
  const { data: runsData } = useAppBackupRuns(hostId, project);

  const backup = useBackupApp(hostId, project);
  const update = useUpdateApp(hostId, project);
  const restore = useRestoreApp(hostId, project);
  const removeSnapshot = useDeleteSnapshot(hostId, project);

  const [targets, setTargets] = useState<Record<string, string>>({});
  const services = plan?.services ?? [];
  const runs = runsData?.runs ?? [];
  const active = runs.find((r) => r.phase === 'queued' || r.phase === 'running');
  const blocked = plan?.blocked ?? status?.reason ?? '';
  const busy = !!active || backup.isPending || update.isPending || restore.isPending;

  const pending: ServiceTarget[] = useMemo(
    () =>
      services
        .filter((s) => targets[s.service] && targets[s.service] !== s.tag)
        .map((s) => ({
          service: s.service,
          current_image: s.image,
          target_image: `${s.repo}:${targets[s.service]}`,
        })),
    [services, targets]
  );

  const worst: MoveSeverity = useMemo(() => {
    const order: MoveSeverity[] = ['downgrade', 'major', 'minor', 'unknown', 'same'];
    let w: MoveSeverity = 'same';
    for (const s of services) {
      const sev = classifyMove(s.tag, targets[s.service] ?? s.tag);
      if (order.indexOf(sev) < order.indexOf(w)) w = sev;
    }
    return w;
  }, [services, targets]);

  async function onBackup() {
    const { confirmed } = await confirmDialog({
      title: `Back up ${project}?`,
      description: (
        <span>
          The application will be <strong>stopped</strong> while its data is snapshotted, then
          started again. {plan?.paths.length ?? 0} data locations ({fmtBytes(plan?.total_size)}) plus
          its compose file will be stored.
        </span>
      ),
      confirmText: 'Back up',
    });
    if (!confirmed) return;
    backup.mutate(undefined, {
      onSuccess: () => toast.success('Backup queued'),
      onError: (e) => toast.error(apiError(e)),
    });
  }

  async function onUpdate() {
    if (!pending.length) return;
    const destructive = worst === 'downgrade' || worst === 'major';
    const lead =
      worst === 'downgrade'
        ? 'This moves at least one service BACKWARDS. Downgrades routinely fail: a newer version may already have migrated the data in a way the older one cannot read.'
        : worst === 'major'
          ? 'This crosses at least one MAJOR version. Major releases are where breaking changes and one-way data migrations live.'
          : 'A snapshot is taken first, so you can roll back from the history below.';
    const { confirmed } = await confirmDialog({
      title: `Update ${project}?`,
      description: (
        <span>
          <span className={destructive ? 'font-medium text-destructive' : undefined}>{lead}</span>
          <br />
          <br />
          {pending.map((t) => (
            <span key={t.service} className="block font-mono text-xs">
              {t.service}: {t.current_image} → {t.target_image}
            </span>
          ))}
          <br />
          The application will be stopped, snapshotted, its compose file rewritten (a{' '}
          <code>.bak</code> is kept) and started on the new images.
        </span>
      ),
      variant: destructive ? 'destructive' : 'default',
      confirmText: destructive ? 'I understand, update' : 'Update',
    });
    if (!confirmed) return;
    update.mutate(pending, {
      onSuccess: () => {
        toast.success('Update queued');
        setTargets({});
      },
      onError: (e) => toast.error(apiError(e)),
    });
  }

  async function onRestore(s: AppSnapshot) {
    const { confirmed } = await confirmDialog({
      title: `Restore ${project} from ${s.short_id}?`,
      description: (
        <span>
          <span className="font-medium text-destructive">
            This ERASES the application's current data
          </span>{' '}
          and writes the snapshot from {fmtTime(s.time)} over it — compose file included, so the
          image versions of that moment come back too. Anything written since then is lost.
        </span>
      ),
      variant: 'destructive',
      confirmText: 'Erase and restore',
    });
    if (!confirmed) return;
    restore.mutate(s.id, {
      onSuccess: () => toast.success('Restore queued'),
      onError: (e) => toast.error(apiError(e)),
    });
  }

  async function onDelete(s: AppSnapshot) {
    const { confirmed } = await confirmDialog({
      title: `Delete snapshot ${s.short_id}?`,
      description: 'The snapshot and the space it holds are removed from the repository. This cannot be undone.',
      variant: 'destructive',
      confirmText: 'Delete',
    });
    if (!confirmed) return;
    removeSnapshot.mutate(s.id, {
      onSuccess: () => toast.success('Snapshot deleted'),
      onError: (e) => toast.error(apiError(e)),
    });
  }

  if (isLoading) return null;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 pb-2">
        <div className="flex items-center gap-2">
          <Archive className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium">Backup & update</span>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={onBackup} disabled={!!blocked || busy}>
            {backup.isPending ? <Loader2 className="mr-1 h-3 w-3 animate-spin" /> : null}
            Back up now
          </Button>
          <Button size="sm" onClick={onUpdate} disabled={!!blocked || busy || !pending.length}>
            {update.isPending ? <Loader2 className="mr-1 h-3 w-3 animate-spin" /> : null}
            Update {pending.length ? `(${pending.length})` : ''}
          </Button>
        </div>
      </CardHeader>

      <CardContent className="space-y-4 pt-0">
        {blocked && (
          <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
            <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
            <div className="space-y-1">
              <div>{blocked}</div>
              {plan && !plan.local && dashboardUrl && (
                <a
                  href={`${dashboardUrl.replace(/\/$/, '')}/applications/${plan.host_id}/${encodeURIComponent(project)}`}
                  className="inline-flex items-center gap-1 font-medium underline"
                  target="_blank"
                  rel="noreferrer"
                >
                  Open that node <ExternalLink className="h-3 w-3" />
                </a>
              )}
            </div>
          </div>
        )}

        {active && (
          <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-3 text-xs">
            <Loader2 className="h-4 w-4 animate-spin" />
            <span className="font-medium capitalize">{active.kind}</span>
            <span className="text-muted-foreground">{active.message || active.phase}</span>
          </div>
        )}

        {!!plan?.paths.length && (
          <div>
            <div className="mb-1 text-xs font-medium text-muted-foreground">
              What gets stored — {plan.paths.length} locations, {fmtBytes(plan.total_size)}
            </div>
            <div className="divide-y divide-border/60 rounded-md border">
              {plan.paths.map((p) => (
                <div key={p.source} className="flex items-center gap-2 px-3 py-1.5 text-xs">
                  <Badge variant="outline" className="text-[10px]">
                    {p.kind}
                  </Badge>
                  <span className="truncate font-mono">{p.source}</span>
                  <span className="ml-auto shrink-0 text-muted-foreground">{fmtBytes(p.size)}</span>
                </div>
              ))}
              {plan.compose_files.map((f) => (
                <div key={f} className="flex items-center gap-2 px-3 py-1.5 text-xs">
                  <Badge variant="outline" className="text-[10px]">
                    compose
                  </Badge>
                  <span className="truncate font-mono">{f}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {!!services.length && (
          <div>
            <div className="mb-1 text-xs font-medium text-muted-foreground">Services</div>
            <div className="divide-y divide-border/60 rounded-md border px-3">
              {services.map((s) => (
                <ServiceRow
                  key={s.service}
                  svc={s}
                  target={targets[s.service] ?? s.tag}
                  onChange={(tag) => setTargets((t) => ({ ...t, [s.service]: tag }))}
                />
              ))}
            </div>
          </div>
        )}

        {!!snaps?.snapshots?.length && (
          <div>
            <div className="mb-1 text-xs font-medium text-muted-foreground">
              Snapshots — {snaps.snapshots.length}
              {(() => {
                const added = snaps.snapshots.reduce((a, x) => a + (x.size_added ?? 0), 0);
                return added ? <span className="font-normal"> · {fmtBytes(added)} in the repository</span> : null;
              })()}
            </div>
            <div className="divide-y divide-border/60 rounded-md border">
              {snaps.snapshots.map((s) => (
                <div key={s.id} className="flex items-center gap-2 px-3 py-1.5 text-xs">
                  <Badge variant="outline" className="font-mono text-[10px]">
                    {s.short_id}
                  </Badge>
                  <span className="shrink-0 text-muted-foreground">{fmtTime(s.time)}</span>
                  <Badge variant="secondary" className="text-[10px] capitalize">
                    {s.kind}
                  </Badge>
                  {!!s.size && (
                    <span
                      className="shrink-0 font-mono text-[10px] text-muted-foreground"
                      title={`${fmtBytes(s.size)} of data; ${fmtBytes(s.size_added)} newly added to the repository`}
                    >
                      {fmtBytes(s.size)}
                      {!!s.size_added && <span className="opacity-60"> (+{fmtBytes(s.size_added)})</span>}
                    </span>
                  )}
                  {/* An update snapshot says what it preceded; a plain backup
                      lists what the project was running. */}
                  {s.moves?.length ? (
                    <span className="hidden truncate font-mono text-[10px] sm:inline">
                      {s.moves.map((m) => `${m.service}: ${shortRef(m.from)} → ${shortRef(m.to)}`).join(', ')}
                    </span>
                  ) : (
                    s.images && (
                      <span className="hidden truncate font-mono text-[10px] text-muted-foreground sm:inline">
                        {Object.values(s.images).join(', ')}
                      </span>
                    )
                  )}
                  <div className="ml-auto flex shrink-0 items-center gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7 px-2"
                      disabled={!!blocked || busy}
                      onClick={() => onRestore(s)}
                    >
                      <RotateCcw className="mr-1 h-3 w-3" />
                      Restore
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7 px-2 text-destructive"
                      disabled={busy}
                      onClick={() => onDelete(s)}
                    >
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {!!runs.length && (
          <div>
            <div className="mb-1 flex items-center gap-1 text-xs font-medium text-muted-foreground">
              <History className="h-3 w-3" /> History
            </div>
            <div className="divide-y divide-border/60 rounded-md border">
              {runs.slice(0, 8).map((r) => (
                <div key={r.job_id} className="flex items-center gap-2 px-3 py-1.5 text-xs">
                  {r.kind === 'restore' ? (
                    <ArrowDownCircle className="h-3 w-3 text-muted-foreground" />
                  ) : (
                    <ArrowUpCircle className="h-3 w-3 text-muted-foreground" />
                  )}
                  <span className="capitalize">{r.kind}</span>
                  <span className="text-muted-foreground">{fmtTime(r.started_at)}</span>
                  <Badge
                    variant="outline"
                    className={cn(
                      'text-[10px] capitalize',
                      r.phase === 'failed' && 'border-destructive/40 bg-destructive/15 text-destructive',
                      r.phase === 'succeeded' && 'border-emerald-500/40 bg-emerald-500/15 text-emerald-600'
                    )}
                  >
                    {r.phase}
                  </Badge>
                  {r.error && <span className="truncate text-destructive">{r.error}</span>}
                </div>
              ))}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
