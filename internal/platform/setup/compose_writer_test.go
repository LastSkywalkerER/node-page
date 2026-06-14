package setup

import (
	"strings"
	"testing"
)

func TestBuildComposeContent_SQLite(t *testing.T) {
	out := BuildComposeContent(DesiredState{DBMode: DBModeSQLite})
	mustContain(t, out, []string{
		"node-stats:",
		"node-stats-controller:",
		`command: ["controller"]`,
		"./.env.agent:/app/.env",
		"./data/docker:/app/data",
		"/var/run/docker.sock:/var/run/docker.sock",
		"${NODE_STATS_STACK_HOST_DIR}:${NODE_STATS_STACK_HOST_DIR}",
		"DB_TYPE=sqlite",
		"DB_DSN=/app/data/stats.db",
		// Resource/logging guards (hub VPS is 2 CPU/4GB).
		"GOMEMLIMIT=${GOMEMLIMIT:-768MiB}",
		// The controller sidecar gets its own tight soft limit so it doesn't
		// out-RSS the app (Go parks freed pages without one).
		"GOMEMLIMIT=128MiB",
		"mem_limit: 1g",
		"stop_grace_period: 30s",
		"driver: json-file",
		`max-size: "10m"`,
		`max-file: "3"`,
	})
	mustNotContain(t, out, []string{
		"  db:", // no postgres service for sqlite
		"DB_TYPE=postgres",
		"JWT_SECRET=", // secrets come from .env.agent, never compose env
	})
}

func TestBuildComposeContent_PostgresManaged(t *testing.T) {
	out := BuildComposeContent(DesiredState{
		DBMode: DBModePostgresManaged,
		DBDSN:  "host=db user=node_stats password=s3cr3t dbname=node_stats port=5432 sslmode=disable",
		DB:     DBProvision{Name: "node_stats", User: "node_stats", Password: "s3cr3t"},
	})
	mustContain(t, out, []string{
		"  db:",
		"image: postgres:16-alpine",
		"DB_TYPE=postgres",
		"host=db user=node_stats password=s3cr3t",
		"POSTGRES_DB=node_stats",
		"POSTGRES_USER=node_stats",
		"POSTGRES_PASSWORD=s3cr3t",
		"depends_on:",
		"condition: service_healthy",
		"pg_isready -U node_stats -d node_stats",
		"volumes:\n  pgdata:",
		// Managed-db resource tuning for the small hub VPS.
		"mem_limit: 768m",
		"shm_size: 256mb",
		"command: postgres -c shared_buffers=${NODE_STATS_PG_SHARED_BUFFERS:-128MB} -c max_connections=${NODE_STATS_PG_MAX_CONNECTIONS:-16} -c wal_compression=on -c checkpoint_completion_target=0.9",
	})
}

func TestBuildComposeContent_PostgresExternal(t *testing.T) {
	out := BuildComposeContent(DesiredState{DBMode: DBModePostgresExternal})
	mustNotContain(t, out, []string{"  db:", "pgdata:"})
	mustContain(t, out, []string{"node-stats:", "node-stats-controller:"})
}

func TestAssembleDSN(t *testing.T) {
	// sqlite passes through
	if got := AssembleDSN(&ConfigValues{DBType: "sqlite", DBDSN: "stats.db"}); got != "stats.db" {
		t.Errorf("sqlite DSN = %q, want stats.db", got)
	}
	// postgres with structured fields builds the keyword form
	got := AssembleDSN(&ConfigValues{
		DBType: "postgres", DBHost: "db", DBUser: "node_stats",
		DBPassword: "pw", DBName: "node_stats", DBPort: "5432", DBSSLMode: "disable",
	})
	want := "host=db user=node_stats password=pw dbname=node_stats port=5432 sslmode=disable"
	if got != want {
		t.Errorf("postgres DSN = %q, want %q", got, want)
	}
	// postgres with a raw DSN (no host) passes through
	if got := AssembleDSN(&ConfigValues{DBType: "postgres", DBDSN: "host=x"}); got != "host=x" {
		t.Errorf("raw postgres DSN = %q, want host=x", got)
	}
}

func mustContain(t *testing.T, s string, subs []string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("output missing %q\n---\n%s", sub, s)
		}
	}
}

func mustNotContain(t *testing.T, s string, subs []string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("output unexpectedly contains %q", sub)
		}
	}
}
