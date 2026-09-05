package setup

import (
	"fmt"
	"reflect"
	"strings"
)

// DB mode identifiers carried in the desired-state descriptor and used by
// BuildComposeContent to decide whether to inject a Postgres service.
const (
	DBModeSQLite           = "sqlite"
	DBModePostgresManaged  = "postgres-managed"  // node-stats runs a `db` postgres container
	DBModePostgresExternal = "postgres-external" // operator points at an existing server
)

// BackupMountPath is where the application-backup repository is mounted inside
// the app container. Fixed on purpose: the operator configures a host path, and
// this is the single in-container path it always lands on.
const BackupMountPath = "/app/backups"

// DefaultImage is the published image the generated compose pulls. Overridable
// per-deployment via the NODE_STATS_IMAGE compose variable.
const DefaultImage = "ghcr.io/lastskywalkerer/node-page:latest"

// DesiredState is the descriptor the running app writes to the shared data
// volume (desired-state.json) and the controller reads to (re)generate and
// apply the docker-compose stack. It is the single source of truth for the
// DB topology and the target image. The app↔controller contract — keep the
// JSON tags stable.
type DesiredState struct {
	// Generation is a monotonically increasing counter; together with the
	// content hash it lets the controller skip already-applied states.
	Generation int `json:"generation"`

	// DBMode is one of the DBMode* constants.
	DBMode string `json:"db_mode"`

	// DBDSN is the app's connection string for the postgres modes (the GORM
	// keyword form; host=db for managed). Empty for sqlite (a fixed in-volume
	// path is used). The controller writes this into the app's DB_DSN env so a
	// DB change actually takes effect on recreate.
	DBDSN string `json:"db_dsn"`

	// DB carries the managed-Postgres provisioning details (mode
	// postgres-managed only) — used to configure the injected `db` container.
	DB DBProvision `json:"db"`

	// Image is the container image both services run (defaults to DefaultImage).
	Image string `json:"image"`

	// PullBeforeApply asks the controller to `docker compose pull` the app
	// service before recreating it (used by the auto-updater).
	PullBeforeApply bool `json:"pull_before_apply"`

	// ManagedExternally disables controller mutation entirely (Dokploy/Traefik).
	ManagedExternally bool `json:"managed_externally"`

	// HTTPPort / RaftPort, when non-empty, are the published host ports the
	// controller writes into the stack .env (NODE_STATS_PORT / NODE_STATS_RAFT_PORT)
	// before recreating, so a port change from the in-app settings takes effect.
	// Empty leaves the installer-managed values untouched.
	HTTPPort string `json:"http_port,omitempty"`
	RaftPort string `json:"raft_port,omitempty"`

	// BackupHostPath, when set, is the host directory the application-backup
	// repository lives in. The controller bind-mounts it into the app at
	// BackupMountPath so restic can reach it: the operator names a path on the
	// machine and node-stats arranges the plumbing, rather than asking them to
	// hand-edit compose. Node-local (a filesystem repository is only reachable
	// from the machine holding it), so it is not part of any replicated state.
	BackupHostPath string `json:"backup_host_path,omitempty"`

	// Gateway, when set and Enabled, injects a Traefik `traefik` service (the
	// cluster gateway / ingress) on this node. Written by the gateway
	// materializer on the node the admin picked as gateway.
	Gateway *GatewayProvision `json:"gateway,omitempty"`
}

// GatewayProvision configures the managed Traefik container.
type GatewayProvision struct {
	Enabled bool `json:"enabled"`
	// HTTPPort / HTTPSPort are the published host ports (0 → 80 / 443).
	HTTPPort  int `json:"http_port,omitempty"`
	HTTPSPort int `json:"https_port,omitempty"`
	// ACME (Let's Encrypt, HTTP-01 on the web entrypoint).
	ACMEEnabled bool   `json:"acme_enabled"`
	ACMEEmail   string `json:"acme_email,omitempty"`
	ACMEStaging bool   `json:"acme_staging,omitempty"`
	// ReadTimeoutSeconds is the entrypoint respondingTimeouts.readTimeout for
	// web + websecure (client → gateway request incl. body, i.e. the upload
	// ceiling). Already resolved by the materializer: 0 = unlimited.
	ReadTimeoutSeconds int `json:"read_timeout_seconds"`
	// AliasHeadersStrategy / EncodedPathPolicy: entrypoint hardening, already
	// resolved (see gateway.Config). Empty = leave Traefik's own defaults
	// (only for old binaries that predate the options).
	AliasHeadersStrategy string `json:"alias_headers_strategy,omitempty"`
	EncodedPathPolicy    string `json:"encoded_path_policy,omitempty"`
	// DockerNetworks are EXISTING (external) Docker networks the Traefik
	// service is attached to in addition to the stack's default network, so
	// containers on them are reachable by name. Already validated.
	DockerNetworks []string `json:"docker_networks,omitempty"`
	// StreamPorts are the raw TCP/UDP ports of enabled stream routes: each
	// becomes an entrypoint (ns-<proto>-<port>) published 1:1 on the host.
	StreamPorts []StreamPort `json:"stream_ports,omitempty"`
}

// StreamPort is one (protocol, port) entrypoint of the managed Traefik.
type StreamPort struct {
	Protocol string `json:"protocol"` // tcp | udp
	Port     int    `json:"port"`
}

// EntryPoint is the Traefik entrypoint name (mirrors gateway.StreamEntryPoint).
func (p StreamPort) EntryPoint() string { return fmt.Sprintf("ns-%s-%d", p.Protocol, p.Port) }

// Equal compares two provisions field by field (the struct holds a slice, so
// == is not available).
func (gw GatewayProvision) Equal(o GatewayProvision) bool {
	if len(gw.DockerNetworks) == 0 {
		gw.DockerNetworks = nil
	}
	if len(o.DockerNetworks) == 0 {
		o.DockerNetworks = nil
	}
	if len(gw.StreamPorts) == 0 {
		gw.StreamPorts = nil
	}
	if len(o.StreamPorts) == 0 {
		o.StreamPorts = nil
	}
	return reflect.DeepEqual(gw, o)
}

// Entrypoint hardening values (gateway.Config.AliasHeadersStrategy /
// EncodedPathPolicy). Traefik: entryPoints.<ep>.http.aliasHeadersStrategy
// (≥ 3.7.12) and entryPoints.<ep>.http.encodedCharacters.* (≥ 3.6.7).
const (
	AliasHeadersDelete = "delete"
	AliasHeadersReject = "reject"
	AliasHeadersKeep   = "keep"

	EncodedPathStrict     = "strict"
	EncodedPathPermissive = "permissive"
	EncodedPathParanoid   = "paranoid"
)

// ValidAliasHeadersStrategy / ValidEncodedPathPolicy accept "" (= default).
func ValidAliasHeadersStrategy(v string) bool {
	return v == "" || v == AliasHeadersDelete || v == AliasHeadersReject || v == AliasHeadersKeep
}

func ValidEncodedPathPolicy(v string) bool {
	return v == "" || v == EncodedPathStrict || v == EncodedPathPermissive || v == EncodedPathParanoid
}

// EncodedCharacterOption is one entryPoints.<ep>.http.encodedCharacters.* flag.
type EncodedCharacterOption struct {
	Name  string // e.g. allowEncodedSlash
	Allow bool
}

// EncodedCharacterOptions maps a policy to Traefik's seven flags. Every flag is
// always emitted: Traefik only stops warning about the defaults once all of
// them are explicit (it still logs one informational WRN at start-up when any
// is false — that is the strict/paranoid choice describing itself).
func EncodedCharacterOptions(policy string) []EncodedCharacterOption {
	// strict: the three that enable path confusion (a sloppy backend decodes
	// them into path separators / string terminators) are rejected.
	strictReject := map[string]bool{"allowEncodedSlash": true, "allowEncodedBackSlash": true, "allowEncodedNullCharacter": true}
	names := []string{"allowEncodedSlash", "allowEncodedBackSlash", "allowEncodedNullCharacter",
		"allowEncodedSemicolon", "allowEncodedPercent", "allowEncodedQuestionMark", "allowEncodedHash"}
	out := make([]EncodedCharacterOption, 0, len(names))
	for _, n := range names {
		allow := true
		switch policy {
		case EncodedPathParanoid:
			allow = false
		case EncodedPathPermissive:
			allow = true
		default: // strict
			allow = !strictReject[n]
		}
		out = append(out, EncodedCharacterOption{Name: n, Allow: allow})
	}
	return out
}

// TraefikEntrypointHardeningFlags returns the CLI flags for one entrypoint
// (empty when the provision carries no hardening, i.e. an old Traefik).
func TraefikEntrypointHardeningFlags(ep string, gw GatewayProvision) []string {
	var out []string
	if gw.AliasHeadersStrategy != "" {
		out = append(out, fmt.Sprintf("--entrypoints.%s.http.aliasHeadersStrategy=%s", ep, gw.AliasHeadersStrategy))
	}
	if gw.EncodedPathPolicy != "" {
		for _, o := range EncodedCharacterOptions(gw.EncodedPathPolicy) {
			out = append(out, fmt.Sprintf("--entrypoints.%s.http.encodedCharacters.%s=%t", ep, o.Name, o.Allow))
		}
	}
	return out
}

// GatewayEnabled reports whether the desired state asks for the Traefik service.
func (ds DesiredState) GatewayEnabled() bool { return ds.Gateway != nil && ds.Gateway.Enabled }

// DefaultTraefikImage is the gateway image (overridable via NODE_STATS_TRAEFIK_IMAGE).
// Pinned to the v3.7 line: the entrypoint hardening flags need ≥ 3.7.12 and a
// moving `v3`/`latest` tag could bring new defaults unannounced. Bump together
// with the native line (engine/native.go nativeTraefikLine).
const DefaultTraefikImage = "traefik:v3.7"

// DBProvision configures the managed Postgres container.
type DBProvision struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// BuildComposeContent renders the canonical base docker-compose.yml from the
// desired state. It mirrors the string-building style of buildEnvFileContent.
//
// Design notes:
//   - Auth secrets (JWT_SECRET/REFRESH_SECRET) are deliberately NOT in the app
//     `environment:` — they load from the bind-mounted .env.agent (/app/.env)
//     the wizard writes, so they survive rebuilds and aren't shadowed by empty
//     compose vars (same pattern as RAFT_*).
//   - DB connection (DB_TYPE/DB_DSN) IS in the app `environment:` and is
//     controller-managed: this is what makes a DB switch take effect on recreate
//     (compose detects the env change) and keeps the bootstrap sqlite DB in the
//     persisted /app/data volume.
//   - Host capabilities (pid/ipc/network_mode host, Linux-only) live in an
//     installer-owned docker-compose.override.yml, which compose auto-merges;
//     the controller never touches the override.
//   - The controller identity-mounts the host stack dir at the SAME path
//     (${NODE_STATS_STACK_HOST_DIR}) so the docker CLI's relative bind paths
//     resolve to real host paths when it talks to the host daemon.
func BuildComposeContent(ds DesiredState) string {
	image := strings.TrimSpace(ds.Image)
	if image == "" {
		image = DefaultImage
	}
	imageRef := fmt.Sprintf("${NODE_STATS_IMAGE:-%s}", image)
	managed := ds.DBMode == DBModePostgresManaged

	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteString("\n") }

	w("# Generated by node-stats — do NOT edit by hand.")
	w("# The controller regenerates this file when the setup wizard changes the")
	w("# database/topology. Host capabilities (pid/ipc/network) live in")
	w("# docker-compose.override.yml, which Compose auto-merges and which this")
	w("# file never overwrites.")
	w("services:")

	// --- app service ---------------------------------------------------------
	w("  node-stats:")
	w("    image: " + imageRef)
	w("    ports:")
	w(`      - "${NODE_STATS_PORT:-9090}:9090"`)
	// Publish the Raft port 1:1 so peers can reach this node's consensus
	// transport at the same port it binds/advertises. Override per-instance via
	// NODE_STATS_RAFT_PORT (and set the wizard's Raft port to match) to run
	// several instances on one host.
	w(`      - "${NODE_STATS_RAFT_PORT:-7000}:${NODE_STATS_RAFT_PORT:-7000}"`)
	w("    volumes:")
	w("      - ./.env.agent:/app/.env")
	w("      - ./data/docker:/app/data")
	// Docker socket (read-only) so the Docker collector / Applications view can
	// query the daemon. The controller mounts it read-write for compose control.
	w("      - /var/run/docker.sock:/var/run/docker.sock:ro")
	w("      - /:/host:ro")
	// The application-backup repository, mounted at a fixed in-container path
	// so the stored configuration is the HOST path an operator recognises.
	if p := strings.TrimSpace(ds.BackupHostPath); p != "" {
		w("      - " + p + ":" + BackupMountPath)
	}
	w("    extra_hosts:")
	w(`      - "host.docker.internal:host-gateway"`)
	if managed {
		w("    depends_on:")
		w("      db:")
		w("        condition: service_healthy")
	}
	w("    environment:")
	w("      - ADDR=:9090")
	w("      - GIN_MODE=${GIN_MODE:-release}")
	// DB connection (controller-managed; JWT/REFRESH come from .env.agent).
	if ds.DBMode == DBModeSQLite {
		w("      - DB_TYPE=sqlite")
		w("      - DB_DSN=/app/data/stats.db")
	} else {
		w("      - DB_TYPE=postgres")
		// In Compose's `environment:` list form the value is everything after the
		// first '='; it must NOT be quoted (quotes become literal). Spaces in the
		// keyword DSN are fine; only '$' needs escaping (Compose interpolation).
		w("      - DB_DSN=" + composeEnvValue(ds.DBDSN))
	}
	w("      - HOST_PROC=/host/proc")
	w("      - HOST_SYS=/host/sys")
	w("      - HOST_ETC=/host/etc")
	w("      - HOST_ROOT=/host")
	w("      - NODE_HOST_ALIAS=host.docker.internal")
	// Host outbound IPv4 detected by the installer (the container can't see it
	// behind a bridge network); empty falls back to the in-container probe.
	w("      - NODE_STATS_IPV4=${NODE_STATS_IPV4:-}")
	// The published Raft port (the installer may auto-pick a non-default one
	// when 7000 is taken). The wizard's bind/advertise defaults follow it so
	// the in-container listener always matches the host mapping.
	w("      - NODE_STATS_RAFT_PORT=${NODE_STATS_RAFT_PORT:-7000}")
	// Optional display hostname — distinguishes nodes that share a host
	// (e.g. several instances on one Docker VM, whose OS hostname collides).
	w("      - NODE_STATS_HOSTNAME=${NODE_STATS_HOSTNAME:-}")
	// Compose project name, so the app can address sibling services of its own
	// stack (e.g. the gateway's `traefik` container logs) via compose labels.
	w("      - NODE_STATS_PROJECT=${COMPOSE_PROJECT_NAME:-node-stats}")
	// The host path of the stack directory, so the app can suggest a default
	// backup location next to itself ("<stack>/backups") instead of asking the
	// operator to work out where the container actually lives.
	w("      - NODE_STATS_STACK_HOST_DIR=${NODE_STATS_STACK_HOST_DIR:-}")
	// Cap the Go heap below the container mem_limit so the runtime GC's harder
	// instead of letting the OOM killer reap the app (the hub VPS is 2 CPU/4GB).
	// Runtime-overridable, so a desired-state / installer env wins over the default.
	w("      - GOMEMLIMIT=${GOMEMLIMIT:-768MiB}")
	w("    mem_limit: 1g")
	// Give the app time to flush/checkpoint and shut down cleanly on recreate.
	w("    stop_grace_period: 30s")
	writeLogging(w)
	w("    restart: unless-stopped")

	// --- managed postgres (optional) -----------------------------------------
	if managed {
		name := orDefault(ds.DB.Name, "node_stats")
		user := orDefault(ds.DB.User, "node_stats")
		pass := ds.DB.Password
		w("  db:")
		w("    image: postgres:16-alpine")
		w("    restart: unless-stopped")
		// Tuned for a small VPS: modest shared_buffers + connections (the app uses
		// only a handful), and wal_compression to shrink WAL volume (the workload is
		// write-heavy: a metric tick per host). Both knobs are overridable from the
		// stack .env (NODE_STATS_PG_SHARED_BUFFERS / NODE_STATS_PG_MAX_CONNECTIONS)
		// so a bigger box can raise them without a code change. shm_size lifts
		// Postgres's parallel-query/sort shared-memory ceiling above Docker's 64MB.
		w("    mem_limit: 768m")
		w("    shm_size: 256mb")
		w("    command: postgres -c shared_buffers=${NODE_STATS_PG_SHARED_BUFFERS:-64MB} -c max_connections=${NODE_STATS_PG_MAX_CONNECTIONS:-16} -c wal_compression=on -c checkpoint_completion_target=0.9")
		w("    environment:")
		w("      - POSTGRES_DB=" + name)
		w("      - POSTGRES_USER=" + user)
		w("      - POSTGRES_PASSWORD=" + composeEnvValue(pass))
		w("    volumes:")
		w("      - pgdata:/var/lib/postgresql/data")
		w("    healthcheck:")
		w(fmt.Sprintf(`      test: ["CMD-SHELL", "pg_isready -U %s -d %s"]`, user, name))
		w("      interval: 5s")
		w("      timeout: 3s")
		w("      retries: 10")
		writeLogging(w)
	}

	// --- gateway: managed Traefik (optional) ----------------------------------
	if ds.GatewayEnabled() {
		writeTraefikService(w, *ds.Gateway)
	}

	// --- controller sidecar --------------------------------------------------
	w("  node-stats-controller:")
	w("    image: " + imageRef)
	w(`    command: ["controller"]`)
	w("    volumes:")
	w("      - /var/run/docker.sock:/var/run/docker.sock")
	// Identity mount: same path inside the controller as on the host so the
	// docker CLI's relative bind-mount sources resolve to real host paths.
	w("      - ${NODE_STATS_STACK_HOST_DIR}:${NODE_STATS_STACK_HOST_DIR}")
	w("    environment:")
	// The controller only polls desired-state.json and shells out to `docker
	// compose`; its own Go heap is tiny. Without a soft memory limit the Go
	// runtime parks freed pages and the sidecar's RSS drifts well above the
	// app's (observed ~2x). A tight GOMEMLIMIT makes the GC return memory
	// promptly — it's a soft target, so a busy compose run can still exceed it.
	w("      - GOMEMLIMIT=128MiB")
	w("      - NODE_STATS_STACK_DIR=${NODE_STATS_STACK_HOST_DIR}")
	w("      - NODE_STATS_DATA_DIR=${NODE_STATS_STACK_HOST_DIR}/data/docker")
	w("      - NODE_STATS_APP_SERVICE=node-stats")
	// The image this stack runs, so the controller can start helper containers
	// on the SAME version rather than the published default. It also asks the
	// daemon what it is running itself; this is the cheap path.
	w("      - NODE_STATS_IMAGE=" + imageRef)
	// The controller drives `docker compose -p <project>`; it must match the
	// stack's project so multi-instance hosts don't cross-manage each other.
	w("      - NODE_STATS_PROJECT=${COMPOSE_PROJECT_NAME:-node-stats}")
	// The host path of the stack directory, so the app can suggest a default
	// backup location next to itself ("<stack>/backups") instead of asking the
	// operator to work out where the container actually lives.
	w("      - NODE_STATS_STACK_HOST_DIR=${NODE_STATS_STACK_HOST_DIR:-}")
	// The image HEALTHCHECK probes the HTTP API, which the controller
	// subcommand does not serve - without this every controller shows
	// a misleading "(unhealthy)" forever.
	w("    healthcheck:")
	w("      disable: true")
	writeLogging(w)
	w("    restart: unless-stopped")

	// --- top-level volumes ---------------------------------------------------
	if managed {
		w("volumes:")
		w("  pgdata:")
	}

	// --- top-level networks: the gateway's extra (external) Docker networks --
	// Declared `external` so compose never creates/removes them — they belong
	// to the other stacks (an NPM network, an app's compose network…).
	if ds.GatewayEnabled() && len(ds.Gateway.DockerNetworks) > 0 {
		w("networks:")
		for _, n := range ds.Gateway.DockerNetworks {
			w("  " + n + ":")
			w("    external: true")
			w("    name: " + n)
		}
	}

	return b.String()
}

// writeTraefikService emits the managed gateway container. Design:
//   - file provider only (node-stats renders ./data/docker/traefik/dynamic/
//     node-stats.yml from the replicated route table; Traefik hot-reloads it);
//     no docker provider, so Traefik never needs the socket or labels.
//   - `web` (:80) / `websecure` (:443) entrypoints published on the configured
//     host ports; `ping` on :8082 (container-internal) feeds the healthcheck and
//     the app's liveness probe (http://traefik:8082/ping on the compose network).
//   - ACME HTTP-01 via resolver `le` when enabled; acme.json persists under
//     ./data/docker/traefik/acme.
//
// RenderTraefikService returns the compose stanza the controller is asked to
// run for the managed gateway (for the admin "config files" viewer).
func RenderTraefikService(gw GatewayProvision) string {
	var b strings.Builder
	writeTraefikService(func(s string) { b.WriteString(s); b.WriteString("\n") }, gw)
	return b.String()
}

func writeTraefikService(w func(string), gw GatewayProvision) {
	httpPort, httpsPort := gw.HTTPPort, gw.HTTPSPort
	if httpPort <= 0 {
		httpPort = 80
	}
	if httpsPort <= 0 {
		httpsPort = 443
	}
	w("  traefik:")
	w(fmt.Sprintf("    image: ${NODE_STATS_TRAEFIK_IMAGE:-%s}", DefaultTraefikImage))
	w("    restart: unless-stopped")
	w("    command:")
	w("      - --providers.file.directory=/etc/traefik/dynamic")
	w("      - --providers.file.watch=true")
	w("      - --entrypoints.web.address=:80")
	w("      - --entrypoints.websecure.address=:443")
	// Traefik v3 defaults readTimeout to 60s and it covers the request BODY,
	// so any upload slower than a minute dies; node-stats owns the value.
	w(fmt.Sprintf("      - --entrypoints.web.transport.respondingTimeouts.readTimeout=%ds", gw.ReadTimeoutSeconds))
	w(fmt.Sprintf("      - --entrypoints.websecure.transport.respondingTimeouts.readTimeout=%ds", gw.ReadTimeoutSeconds))
	w("      - --entrypoints.ping.address=:8082")
	// Stream routes: one raw TCP/UDP entrypoint per (protocol, port).
	for _, p := range gw.StreamPorts {
		w(fmt.Sprintf("      - --entrypoints.%s.address=:%d/%s", p.EntryPoint(), p.Port, p.Protocol))
	}
	// Entrypoint hardening (alias headers, encoded path characters) — on every
	// http entrypoint incl. ping, so Traefik's start-up warnings go quiet.
	for _, ep := range []string{"web", "websecure", "ping"} {
		for _, f := range TraefikEntrypointHardeningFlags(ep, gw) {
			w("      - " + f)
		}
	}
	w("      - --ping=true")
	w("      - --ping.entryPoint=ping")
	w("      - --api.dashboard=false")
	w("      - --log.level=${NODE_STATS_TRAEFIK_LOG_LEVEL:-INFO}")
	// JSON access log to a file on the shared data volume: the app tails it for
	// the Gateway tab's connection stats (and truncates it at a size cap, so
	// disk stays bounded). Docker stdout keeps the plain traefik log only.
	w("      - --accesslog=true")
	w("      - --accesslog.format=json")
	w("      - --accesslog.filepath=/var/log/traefik/access.log")
	w("      - --accesslog.fields.headers.names.User-Agent=keep")
	w("      - --global.sendAnonymousUsage=false")
	w("      - --global.checkNewVersion=false")
	if gw.ACMEEnabled {
		w("      - --certificatesresolvers.le.acme.email=" + composeEnvValue(gw.ACMEEmail))
		w("      - --certificatesresolvers.le.acme.storage=/letsencrypt/acme.json")
		w("      - --certificatesresolvers.le.acme.httpchallenge=true")
		w("      - --certificatesresolvers.le.acme.httpchallenge.entrypoint=web")
		if gw.ACMEStaging {
			w("      - --certificatesresolvers.le.acme.caserver=https://acme-staging-v02.api.letsencrypt.org/directory")
		}
	}
	// Targets published on THIS host are addressed as host.docker.internal
	// (the app rewrites the gateway node's own IP to it), which needs the
	// host-gateway alias inside the Traefik container too.
	w("    extra_hosts:")
	w(`      - "host.docker.internal:host-gateway"`)
	// Extra networks: listing any network on a service drops the implicit
	// default, so it is re-added first (the app's liveness probe reaches
	// traefik:8082 over it).
	if len(gw.DockerNetworks) > 0 {
		w("    networks:")
		w("      - default")
		for _, n := range gw.DockerNetworks {
			w("      - " + n)
		}
	}
	w("    ports:")
	w(fmt.Sprintf(`      - "%d:80"`, httpPort))
	w(fmt.Sprintf(`      - "%d:443"`, httpsPort))
	for _, p := range gw.StreamPorts {
		w(fmt.Sprintf(`      - "%d:%d/%s"`, p.Port, p.Port, p.Protocol))
	}
	w("    volumes:")
	w("      - ./data/docker/traefik/dynamic:/etc/traefik/dynamic:ro")
	w("      - ./data/docker/traefik/acme:/letsencrypt")
	w("      - ./data/docker/traefik/logs:/var/log/traefik")
	// `traefik healthcheck` does not read the running instance's flags — without
	// the ping entrypoint repeated here it probes :8080 and the container stays
	// "unhealthy" forever (and `up --wait` fails).
	w("    healthcheck:")
	w(`      test: ["CMD", "traefik", "healthcheck", "--ping", "--ping.entryPoint=ping", "--entrypoints.ping.address=:8082"]`)
	w("      interval: 10s")
	w("      timeout: 3s")
	w("      retries: 6")
	w("    mem_limit: 256m")
	writeLogging(w)
}

// writeLogging emits a json-file logging stanza (indented for a service block)
// that caps container logs so the small hub VPS doesn't fill its disk with
// unbounded log files. 10MB × 3 files = 30MB per service.
func writeLogging(w func(string)) {
	w("    logging:")
	w("      driver: json-file")
	w("      options:")
	w(`        max-size: "10m"`)
	w(`        max-file: "3"`)
}

// orDefault returns v trimmed, or def when empty.
func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

// composeEnvValue escapes a value for use in a Compose `environment:` list item
// (`- KEY=VALUE`). The value must stay unquoted there (surrounding quotes become
// literal); only '$' needs doubling so Compose doesn't treat it as interpolation.
func composeEnvValue(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}
