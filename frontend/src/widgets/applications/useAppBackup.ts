import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '@/shared/lib/api';

export interface BackupPath {
  kind: 'volume' | 'bind' | string;
  name?: string;
  source: string;
  services?: string[];
  size?: number;
}

export interface ServiceState {
  service: string;
  image: string;
  repo: string;
  tag: string;
  containers: string[];
  update_available: boolean;
}

export interface AppBackupPlan {
  project: string;
  host_id: number;
  local: boolean;
  compose_files: string[];
  project_dir: string;
  paths: BackupPath[];
  services: ServiceState[];
  total_size: number;
  /** Why the actions are unavailable; empty when they are available. */
  blocked?: string;
}

export interface AppBackupStatus {
  repo_configured: boolean;
  repo?: { backend: string; url: string } & Record<string, unknown>;
  controller_ready: boolean;
  restic_installed: boolean;
  restic_version?: string;
  reason?: string;
}

export interface SnapshotMove {
  service: string;
  from: string;
  to: string;
}

export interface AppSnapshot {
  id: string;
  short_id: string;
  time: string;
  hostname: string;
  project: string;
  kind: string;
  paths: string[];
  tags: string[];
  images?: Record<string, string>;
  /** Set on an update snapshot: the version changes it preceded. */
  moves?: SnapshotMove[];
  /** Data the snapshot holds (restore size). */
  size?: number;
  /** What it newly cost the repository after deduplication. */
  size_added?: number;
}

export interface RepoStats {
  total_size: number;
  uncompressed_size: number;
  compression_ratio: number;
  snapshot_count: number;
}

/**
 * Repository totals. Separate from the status query because restic walks the
 * index to answer, so it is refreshed lazily rather than on every poll.
 */
export function useRepoStats(enabled = true) {
  return useQuery<RepoStats>({
    queryKey: ['app-backup-repo-stats'],
    queryFn: async () => (await apiClient.get<RepoStats>('/app-backup/repo/stats')).data,
    enabled,
    staleTime: 5 * 60_000,
    retry: false,
  });
}

export interface AppBackupRun {
  id: number;
  job_id: string;
  host_id: number;
  project: string;
  kind: string;
  phase: 'queued' | 'running' | 'succeeded' | 'failed' | string;
  snapshot_id?: string;
  message?: string;
  error?: string;
  started_at: string;
  finished_at?: string;
}

export interface ServiceTarget {
  service: string;
  current_image: string;
  target_image: string;
}

/** Whether backups can run on this node at all, and why not when they cannot. */
export function useAppBackupStatus() {
  return useQuery<AppBackupStatus>({
    queryKey: ['app-backup-status'],
    queryFn: async () => (await apiClient.get<AppBackupStatus>('/app-backup/status')).data,
    staleTime: 30_000,
  });
}

/** What would be backed up for one application: files, data, services. */
export function useAppBackupPlan(hostId: number, project: string, enabled = true) {
  return useQuery<AppBackupPlan>({
    queryKey: ['app-backup-plan', hostId, project],
    queryFn: async () =>
      (
        await apiClient.get<AppBackupPlan>(
          `/app-backup/${encodeURIComponent(project)}/plan?host_id=${hostId}`
        )
      ).data,
    enabled,
    staleTime: 15_000,
  });
}

export function useAppSnapshots(project: string, enabled = true) {
  return useQuery<{ snapshots: AppSnapshot[] }>({
    queryKey: ['app-backup-snapshots', project],
    queryFn: async () =>
      (
        await apiClient.get<{ snapshots: AppSnapshot[] }>(
          `/app-backup/${encodeURIComponent(project)}/snapshots`
        )
      ).data,
    enabled,
    staleTime: 10_000,
  });
}

/**
 * Run history. Polls quickly while a job is queued or running — the controller
 * reports progress through a file the server folds in on each read — and backs
 * off to a slow refresh once everything has settled.
 */
export function useAppBackupRuns(hostId: number, project: string, enabled = true) {
  return useQuery<{ runs: AppBackupRun[] }>({
    queryKey: ['app-backup-runs', hostId, project],
    queryFn: async () =>
      (
        await apiClient.get<{ runs: AppBackupRun[] }>(
          `/app-backup/${encodeURIComponent(project)}/runs?host_id=${hostId}`
        )
      ).data,
    enabled,
    refetchInterval: (q) => {
      const runs = q.state.data?.runs ?? [];
      return runs.some((r) => r.phase === 'queued' || r.phase === 'running') ? 3000 : 30_000;
    },
    staleTime: 0,
  });
}

/** Tags the image's registry publishes, versions first. */
export function useImageVersions(image: string, enabled: boolean) {
  return useQuery<{ tags: string[] }>({
    queryKey: ['app-backup-versions', image],
    queryFn: async () =>
      (await apiClient.get<{ tags: string[] }>(`/app-backup/versions?image=${encodeURIComponent(image)}`)).data,
    enabled: enabled && !!image,
    staleTime: 5 * 60_000,
    retry: false,
  });
}

/** Invalidates everything a finished job could have changed. */
function useRefresh(hostId: number, project: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ['app-backup-runs', hostId, project] });
    qc.invalidateQueries({ queryKey: ['app-backup-snapshots', project] });
    qc.invalidateQueries({ queryKey: ['app-backup-plan', hostId, project] });
    qc.invalidateQueries({ queryKey: ['application-detail', hostId, project] });
  };
}

export function useBackupApp(hostId: number, project: string) {
  const refresh = useRefresh(hostId, project);
  return useMutation<AppBackupRun>({
    mutationFn: async () =>
      (
        await apiClient.post<AppBackupRun>(
          `/app-backup/${encodeURIComponent(project)}/backup?host_id=${hostId}`
        )
      ).data,
    onSuccess: refresh,
  });
}

export function useUpdateApp(hostId: number, project: string) {
  const refresh = useRefresh(hostId, project);
  return useMutation<AppBackupRun, unknown, ServiceTarget[]>({
    mutationFn: async (targets) =>
      (
        await apiClient.post<AppBackupRun>(
          `/app-backup/${encodeURIComponent(project)}/update?host_id=${hostId}`,
          { targets }
        )
      ).data,
    onSuccess: refresh,
  });
}

export function useRestoreApp(hostId: number, project: string) {
  const refresh = useRefresh(hostId, project);
  return useMutation<AppBackupRun, unknown, string>({
    mutationFn: async (snapshotId) =>
      (
        await apiClient.post<AppBackupRun>(
          `/app-backup/${encodeURIComponent(project)}/restore?host_id=${hostId}`,
          { snapshot_id: snapshotId }
        )
      ).data,
    onSuccess: refresh,
  });
}

export function useDeleteSnapshot(hostId: number, project: string) {
  const refresh = useRefresh(hostId, project);
  return useMutation<void, unknown, string>({
    mutationFn: async (snapshotId) => {
      await apiClient.delete(`/app-backup/snapshots/${encodeURIComponent(snapshotId)}`);
    },
    onSuccess: refresh,
  });
}

/**
 * Classifies a version move so the UI can warn proportionally: a downgrade and
 * a major bump are the two that routinely break an application (migrations that
 * do not reverse, dropped config), everything else is routine.
 */
export type MoveSeverity = 'downgrade' | 'major' | 'minor' | 'same' | 'unknown';

export function classifyMove(from: string, to: string): MoveSeverity {
  if (from === to) return 'same';
  const a = parseVersion(from);
  const b = parseVersion(to);
  if (!a || !b) return 'unknown';
  for (let i = 0; i < 3; i++) {
    if (b[i] === a[i]) continue;
    if (b[i] < a[i]) return 'downgrade';
    return i === 0 ? 'major' : 'minor';
  }
  return 'same';
}

/** Parses a leading semver-ish tag ("v1.2.3", "0.26.4", "16"); null otherwise. */
function parseVersion(tag: string): [number, number, number] | null {
  const m = /^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?/.exec(tag.trim());
  if (!m) return null;
  return [Number(m[1]), Number(m[2] ?? 0), Number(m[3] ?? 0)];
}
