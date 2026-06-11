/**
 * Default configuration values for setup wizard
 */
export const DEFAULT_SETUP_CONFIG = {
  jwt_secret: '',
  refresh_secret: '',
  addr: ':8080',
  gin_mode: 'release',
  debug: 'false',
  db_type: 'sqlite',
  db_sqlite_path: 'stats.db',
  db_managed: true,
  db_host: 'db',
  db_port: '5432',
  db_name: 'node_stats',
  db_user: 'node_stats',
  db_password: '',
  db_sslmode: 'disable',
  prometheus_enabled: 'false',
  prometheus_auth: 'false',
  prometheus_token: '',
  docker_host_metrics_compat: false,
  node_stats_hostname: '',
  node_stats_ipv4: '',
  // Raft cluster sync defaults; the wizard's Raft section pre-fills these.
  // Enabled by default so a fresh node is cluster-ready (bootstrap leader)
  // and can immediately hand out a connect key for additional nodes.
  raft_enabled: 'true',
  // Filled by ConfigFormWidget with a per-site unique id ("<host>-<rand>").
  // A constant here breaks the metrics uplink: the hub rejects batches
  // whose sender cluster id equals its own.
  raft_cluster_id: '',
  raft_node_id: '',
  raft_port: '7000',
  raft_advertise_host: '',
  // Empty → the backend resolves it (absolute /app/data/raft in Docker;
  // a relative ./data/raft breaks under orchestrators with a different cwd).
  raft_data_dir: '',
  raft_bootstrap: 'true',
  raft_advertise_public_url: '',
  raft_bridge_enabled: 'false',
  raft_bridge_shared_secret: '',
  raft_bridge_remote_seeds: '',
} as const;

export type DefaultSetupConfig = typeof DEFAULT_SETUP_CONFIG;

