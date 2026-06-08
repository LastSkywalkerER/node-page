import { Boxes, Server } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { useApplications } from './useApplications';
import { ApplicationTile } from './ApplicationTile';
import type { DockerApplication } from './schemas';
import { useHosts } from '@/widgets/hosts/useHosts';
import type { Host } from '@/widgets/hosts/schemas';
import { getHostCardTitle } from '@/shared/lib/hostDisplay';

/**
 * Applications aggregated across every node, grouped by host so you can see
 * where each app is deployed. Feeds the main screen's Applications section.
 */
export function AllApplicationsSection() {
  const { data, isLoading } = useApplications(); // no hostId → all hosts
  const { data: hostsData } = useHosts();
  const hosts: Host[] = hostsData?.hosts ?? [];
  const apps = data?.applications ?? [];

  const byHost = new Map<number, DockerApplication[]>();
  for (const app of apps) {
    const hid = app.host_id ?? 0;
    const list = byHost.get(hid) ?? [];
    list.push(app);
    byHost.set(hid, list);
  }

  // Preserve the host ordering from /hosts (local collector first), then any
  // app groups whose host is unknown.
  const orderedHostIds = [
    ...hosts.map((h) => h.id).filter((id) => byHost.has(id)),
    ...[...byHost.keys()].filter((id) => !hosts.some((h) => h.id === id)),
  ];

  return (
    <section>
      <div className="mb-5 flex items-center gap-3">
        <Boxes className="h-6 w-6 text-primary drop-shadow-[0_0_10px_oklch(0.72_0.16_195/0.45)]" />
        <h2 className="font-display text-xl font-semibold uppercase tracking-wide md:text-2xl">
          Applications
        </h2>
        {!isLoading && (
          <Badge
            variant="secondary"
            className="border border-border/60 bg-primary/10 font-mono tabular-nums text-primary dark:border-cyan-500/25 dark:bg-cyan-500/10 dark:text-cyan-200"
          >
            {apps.length}
          </Badge>
        )}
      </div>

      {isLoading && apps.length === 0 ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-32 w-full rounded-xl" />
          ))}
        </div>
      ) : apps.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border/70 bg-card/30 px-6 py-16 text-center backdrop-blur-md dark:border-white/15">
          <Boxes className="mb-4 h-12 w-12 text-muted-foreground/25" />
          <p className="font-display tracking-wide text-muted-foreground">No applications detected.</p>
          <p className="mt-2 max-w-sm font-mono text-xs text-muted-foreground/70">
            Deploy a compose stack or container on a node to see it here.
          </p>
        </div>
      ) : (
        <div className="space-y-6">
          {orderedHostIds.map((hid) => {
            const host = hosts.find((h) => h.id === hid);
            const hostApps = byHost.get(hid) ?? [];
            const label = host ? getHostCardTitle(host) : `Host #${hid}`;
            return (
              <div key={hid}>
                <div className="mb-2.5 flex items-center gap-2">
                  <Server className="h-3.5 w-3.5 text-muted-foreground" />
                  <span className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
                    {label}
                  </span>
                  <span className="text-[11px] text-muted-foreground/50">·</span>
                  <span className="text-[11px] text-muted-foreground/70">{hostApps.length}</span>
                </div>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                  {hostApps.map((app) => (
                    <ApplicationTile
                      key={`${hid}:${app.project}`}
                      app={app}
                      hostId={hid}
                      hostIPv4={host?.ipv4}
                    />
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
