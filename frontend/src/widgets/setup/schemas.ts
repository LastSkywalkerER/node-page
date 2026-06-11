// Validation schemas for setup wizard
import { z } from 'zod';

const optionalIPv4 = z.string().refine(
  (s) => {
    const t = s.trim()
    if (t === '') return true
    return /^(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)$/.test(t)
  },
  { message: 'Invalid IPv4 address' }
)

// optionalPort accepts an empty string OR a number 1..65535.
const optionalPort = z.string().refine(
  (s) => {
    const t = s.trim()
    if (t === '') return true
    const n = Number(t)
    return Number.isInteger(n) && n >= 1 && n <= 65535
  },
  { message: 'Port must be a number between 1 and 65535' }
)

export const setupConfigSchema = z.object({
  jwt_secret: z.string().min(16, 'JWT secret must be at least 16 characters'),
  refresh_secret: z.string().min(16, 'Refresh secret must be at least 16 characters'),
  addr: z.string(),
  gin_mode: z.enum(['debug', 'release'], { message: 'Gin mode must be debug or release' }),
  debug: z.enum(['true', 'false'], { message: 'Debug must be true or false' }),
  db_type: z.enum(['sqlite', 'postgres'], { message: 'Database must be sqlite or postgres' }),
  // SQLite file path (sqlite only).
  db_sqlite_path: z.string(),
  // Postgres: managed → node-stats runs a `db` container; external → connect to
  // an existing server. The structured fields are assembled into a DSN client-
  // side so the backend contract stays {db_type, db_dsn, db_managed, db_*}.
  db_managed: z.boolean(),
  db_host: z.string(),
  db_port: optionalPort,
  db_name: z.string(),
  db_user: z.string(),
  db_password: z.string(),
  db_sslmode: z.enum(['disable', 'require', 'verify-ca', 'verify-full']),
  prometheus_enabled: z.enum(['true', 'false']),
  prometheus_auth: z.enum(['true', 'false']),
  prometheus_token: z.string(),
  docker_host_metrics_compat: z.boolean(),
  node_stats_hostname: z.string(),
  node_stats_ipv4: optionalIPv4,
  // Raft cluster sync (all fields optional; only used when raft_enabled === 'true').
  // raft_port replaces the previous separate raft_bind_addr / raft_advertise_addr —
  // the same port is used for both. raft_advertise_host is the externally-routable
  // hostname/IP peers should dial; empty means "let the host figure it out".
  raft_enabled: z.enum(['true', 'false']),
  raft_cluster_id: z.string(),
  raft_node_id: z.string(),
  raft_port: optionalPort,
  raft_advertise_host: z.string(),
  raft_data_dir: z.string(),
  raft_bootstrap: z.enum(['true', 'false']),
  raft_advertise_public_url: z.string(),
  raft_bridge_enabled: z.enum(['true', 'false']),
  raft_bridge_shared_secret: z.string(),
  raft_bridge_remote_seeds: z.string(),
}).superRefine((cfg, ctx) => {
  if (cfg.db_type === 'sqlite') {
    if (!cfg.db_sqlite_path.trim()) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['db_sqlite_path'], message: 'SQLite file path is required (e.g. stats.db)' });
    }
    return;
  }
  // postgres: external needs real connection details; managed auto-provisions them.
  if (!cfg.db_managed) {
    if (!cfg.db_host.trim()) ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['db_host'], message: 'Host is required' });
    if (!cfg.db_name.trim()) ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['db_name'], message: 'Database name is required' });
    if (!cfg.db_user.trim()) ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['db_user'], message: 'User is required' });
    if (!cfg.db_password) ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['db_password'], message: 'Password is required' });
  }
});

const passwordSchema = z
  .string()
  .min(8, 'Password must be at least 8 characters')
  .regex(/^(?=.*[a-zA-Z])(?=.*\d)/, 'Password must contain at least one letter and one number');

export const adminUserSchema = z.object({
  email: z.string().email('Invalid email address'),
  password: passwordSchema,
  confirmPassword: z.string(),
}).refine((data) => data.password === data.confirmPassword, {
  message: 'Passwords must match',
  path: ['confirmPassword'],
});

export const completeSetupSchema = z.object({
  config: setupConfigSchema,
  admin_email: z.string().email('Invalid email address'),
  admin_password: passwordSchema,
});

export type SetupConfigFormData = z.infer<typeof setupConfigSchema>;
export type AdminUserFormData = z.infer<typeof adminUserSchema>;
export type CompleteSetupFormData = z.infer<typeof completeSetupSchema>;

export interface MachineHintsResponse {
  suggested_hostname: string;
  suggested_ipv4: string;
  /** Raft port this deployment publishes (installer may auto-pick ≠7000). */
  suggested_raft_port?: string;
}

export interface SetupStatusResponse {
  setup_needed: boolean;
  running_in_docker?: boolean;
  managed_externally?: boolean;
  machine_hints: MachineHintsResponse;
}

export interface ConfigResponse {
  config: {
    jwt_secret: string;
    refresh_secret: string;
    addr: string;
    gin_mode: string;
    debug: string;
    db_type: string;
    db_dsn: string;
    prometheus_enabled: string;
    prometheus_auth: string;
    prometheus_token: string;
    docker_host_metrics_compat: boolean;
    node_stats_hostname: string;
    node_stats_ipv4: string;
    raft_enabled?: string;
    raft_cluster_id?: string;
    raft_node_id?: string;
    raft_bind_addr?: string;
    raft_advertise_addr?: string;
    raft_data_dir?: string;
    raft_bootstrap?: string;
    raft_advertise_public_url?: string;
    raft_bridge_enabled?: string;
    raft_bridge_shared_secret?: string;
    raft_bridge_remote_seeds?: string;
  };
}

export interface CompleteSetupResponse {
  message: string;
  /** True when the controller must recreate the stack onto a new DB before the
   *  admin can be created — the frontend waits for the restart then re-submits. */
  restart_pending?: boolean;
  db_mode?: string;
}

/** API body.config shape (snake_case) for setup preview and complete. */
export function toSetupConfigApiPayload(config: SetupConfigFormData) {
  // Derive RAFT_BIND_ADDR / RAFT_ADVERTISE_ADDR from the single "port"
  // field plus optional "advertise host" the wizard collects, so the
  // operator can't fat-finger the two values into a mismatch. Empty port
  // falls back to the well-known default 7000.
  const port = (config.raft_port || '').trim() || '7000'
  const adHost = (config.raft_advertise_host || '').trim()
  const bindAddr = `:${port}`
  const advertiseAddr = adHost ? `${adHost}:${port}` : bindAddr
  return {
    jwt_secret: config.jwt_secret,
    refresh_secret: config.refresh_secret,
    addr: config.addr || ':8080',
    gin_mode: config.gin_mode || 'release',
    debug: config.debug || 'false',
    db_type: config.db_type || 'sqlite',
    db_managed: config.db_type === 'postgres' ? config.db_managed : false,
    // For sqlite, db_dsn is the file path; for external postgres we send the
    // structured fields and the backend assembles the keyword DSN (managed
    // postgres ignores them — the backend forces host=db and generates a pw).
    db_dsn: config.db_type === 'sqlite' ? (config.db_sqlite_path || 'stats.db') : '',
    db_host: config.db_host.trim(),
    db_port: (config.db_port || '').trim(),
    db_name: config.db_name.trim(),
    db_user: config.db_user.trim(),
    db_password: config.db_password,
    db_sslmode: config.db_sslmode || 'disable',
    prometheus_enabled: config.prometheus_enabled || 'false',
    prometheus_auth: config.prometheus_auth || 'false',
    prometheus_token: config.prometheus_token || '',
    docker_host_metrics_compat: config.docker_host_metrics_compat,
    node_stats_hostname: config.node_stats_hostname.trim(),
    node_stats_ipv4: config.node_stats_ipv4.trim(),
    raft_enabled: config.raft_enabled || 'false',
    raft_cluster_id: config.raft_cluster_id.trim(),
    raft_node_id: config.raft_node_id.trim(),
    raft_bind_addr: bindAddr,
    raft_advertise_addr: advertiseAddr,
    raft_data_dir: config.raft_data_dir.trim(),
    raft_bootstrap: config.raft_bootstrap || 'false',
    raft_advertise_public_url: config.raft_advertise_public_url.trim(),
    raft_bridge_enabled: config.raft_bridge_enabled || 'false',
    raft_bridge_shared_secret: config.raft_bridge_shared_secret.trim(),
    raft_bridge_remote_seeds: config.raft_bridge_remote_seeds.trim(),
  };
}
