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
  db_dsn: 'stats.db',
} as const;

export type DefaultSetupConfig = typeof DEFAULT_SETUP_CONFIG;

