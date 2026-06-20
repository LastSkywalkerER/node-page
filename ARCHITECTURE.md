# node-stats — Architecture Reference

Self-hosted system monitoring app. Go backend (Gin, GORM, gopsutil) + React frontend (Vite, Tailwind v4, shadcn/ui). Collects CPU, memory, disk, network, Docker, sensor metrics per host; stores history in SQLite or PostgreSQL; streams live data via SSE.

---

## Backend

### Tech stack
- **Go 1.24**, Gin, GORM, gopsutil, Docker SDK
- **DB**: SQLite (default) or PostgreSQL — dialect-agnostic via helpers
- **Auth**: JWT in HttpOnly cookies; middleware falls back to `Authorization` header
- **Entry point**: `cmd/server/main.go` → `internal/app/di/container.go` wires everything

### Module pattern (repeat for every new module)
```
internal/modules/{name}/
├── presentation/          # Gin handlers — no business logic
├── application/           # Service interface + implementation
└── infrastructure/
    ├── collectors/        # Data collection (gopsutil / Docker API)
    ├── entities/          # GORM models
    └── repositories/      # Repository interface + GORM implementation
```
Existing modules: `cpu`, `memory`, `disk`, `network`, `docker`, `sensors`, `hosts`, `users`, `history_metrics`, `setup`, `health`, `system`, `stream`, `connectors` (+ `proxmox` under `internal/metrics`).

### Hard rules
1. **Handlers depend only on the Service interface** — never on a repository directly.
2. **All dependencies wired in `internal/app/di/container.go`** — no `new()` outside DI.
3. **Migrations only in `internal/app/database/migrations.go`** — never in constructors.
4. **Routes only in `internal/app/server/server.go`**.
5. **Dialect-agnostic time queries** — use `database.TimeOffsetQuery(db, hours)` and `database.TimeOffsetQueryWithHost(db, hostId, hours)` from `internal/app/database/dialect.go`.
6. **New module tests** mock via repository interfaces listed below.

### Key files
| File | Purpose |
|------|---------|
| `internal/app/di/container.go` | DI wiring |
| `internal/app/server/server.go` | All routes |
| `internal/app/database/migrations.go` | All migrations |
| `internal/app/database/dialect.go` | `TimeOffsetQuery` / `TimeOffsetQueryWithHost` |
| `internal/app/middleware/auth.go` | `AuthJWT` middleware |
| `internal/app/middleware/ratelimit.go` | Rate limiting |
| `internal/app/stream/broker.go` | SSE broker |
| `internal/app/retention/service.go` | Data retention cleanup (runs hourly) |
| `internal/modules/history_metrics/core/service.go` | Periodic collection every 5 s |
| `users/application/token_service.go` | Refresh tokens hashed with SHA-256 |
| `internal/platform/connectors/` | External data-source registry (Proxmox detection, CRUD, AES-GCM secrets) |
| `internal/metrics/proxmox/` | PVE API client + leader-only poller (topology + metrics) |
| `internal/metrics/pbs/` | Proxmox Backup Server API client + leader-only poller (node vitals + datastore/backup health) |

### Repository interfaces (use for test mocks)
- `cpu/infrastructure/repositories.CPURepository`
- `memory/infrastructure/repositories.MemoryRepository`
- `disk/infrastructure/repositories.DiskRepository`
- `network/infrastructure/repositories.NetworkRepository`
- `docker/domain/repositories.DockerRepository`
- `hosts/infrastructure/repositories.HostRepository`
- `users/infrastructure/repositories.UserRepository`
- `users/infrastructure/repositories.RefreshTokenRepository`

### API routes (`/api/v1` prefix)

**Public:**
```
GET  /health
GET  /version               # build identity + update state (current/latest/update_available/auto_update/deployment)
GET  /setup/status          # machine_hints + running_in_docker + managed_externally while setup_needed
GET  /setup/config
POST /setup/preview-env
POST /setup/complete         # returns restart_pending when a DB-engine switch needs a controller recreate
POST /setup/db/test          # pre-flight an external Postgres DSN (no persist/migrate)
POST /auth/register
POST /auth/login
POST /auth/refresh
```

**Protected (AuthJWT required):**
```
POST   /auth/logout
GET    /users/me
GET    /users
PATCH  /users/:id
DELETE /users/:id
GET    /metrics/current
GET    /cpu
GET    /memory
GET    /disk
GET    /network
GET    /docker
GET    /sensors
GET    /hosts
GET    /hosts/current
POST   /hosts/register
GET    /stream              # SSE
POST   /settings/auto-update   # admin; toggle auto-update (persists AUTO_UPDATE to .env.agent)
POST   /settings/release-channel # admin; switch update line stable|beta (persists NODE_STATS_RELEASE_CHANNEL, re-checks)
POST   /settings/update-now    # admin; apply latest now (docker → controller; native → self-replace; managed-externally → deploy webhook)
GET    /settings/deploy-webhook  # admin; read the orchestrator deploy-webhook URL (token-bearing — never exposed via /version)
POST   /settings/deploy-webhook  # admin; save/clear it (persists NODE_STATS_DEPLOY_WEBHOOK_URL)
GET    /connectors             # admin; environment-detection hints + configured connectors
POST   /connectors             # admin; connect Proxmox (validates token, encrypts secret, replicates)
POST   /connectors/proxmox/test  # admin; pre-flight creds → preview (nodes/guests/matched hosts)
POST   /connectors/pbs         # admin; connect Proxmox Backup Server (validates token, encrypts secret, replicates)
POST   /connectors/pbs/test    # admin; pre-flight PBS creds → preview (node/datastores)
POST   /connectors/pexels      # admin; save the dynamic-wallpaper connector (key validated + encrypted)
GET    /wallpaper              # any user; Pexels proxy — current background (?mode=dark|light, rotates every 5 min)
GET    /pbs                    # any user; PBS datastore capacity + backup health for ?host_id= (polled snapshot, replicated over the metric stream to peers + bridged hub)
PATCH  /connectors/:id         # admin; enable/disable
DELETE /connectors/:id         # admin; ?remove_hosts=true also drops connector-only host rows
POST   /connectors/:id/sync    # admin; force a poller resync
```
All metric endpoints accept `?hours=<float>` (default `0.0833` ≈ 5 min) and `?host_id=<uint>`. **`host_id=0` means this server instance** (resolved via current host MAC). Latest and history are always scoped to that host row; unknown `host_id` returns empty payloads (`latest: null`, empty history). **Metrics are replicated cluster-wide via Raft (`CmdMetricBatch`)**, so any node serves any host's CPU/mem/disk/net/docker history (and a host's data survives it going offline). The frontend is uniform: one REST load per metric on mount, then a **single SSE stream for every host** — the node publishes its own host's metrics each cycle *and* every replicated peer's metrics (`applyMetricBatch` → `broker.Publish`), and the client keeps events whose `collecting_host_id` matches the viewed host. The browser only ever talks to its own node; nodes sync over Raft. **Sensors are not replicated** (`/sensors` returns empty for remote hosts). See [docs/CLUSTER.md](docs/CLUSTER.md) for the full data-flow.

**Connectors & host topology (Proxmox).** Hosts carry topology columns (`host_type`, `parent_mac`, `source`, `external_id`, `guest_status`). A configured **Proxmox connector** (admin → Connectors tab) is polled by the Raft leader (or the standalone node) every 10 s: the PVE node becomes a `hypervisor` host row, guests become `vm`/`lxc` children, and **guests are matched to already-registered agent hosts by NIC MAC** (`UpsertConnectorHost`) so nothing is duplicated — an agent row only gains topology (`agent+connector`), while agent-less guests get connector-fed metrics through the normal `CmdMetricBatch` pipeline. Connector-only guest/node rows that disappear from the source (e.g. a VM/LXC deleted in Proxmox) are pruned each cycle (`FindConnectorOnlyHostsByExternalIDPrefix` vs the live set; agent rows are never auto-deleted). Credential-free probes (DMI / `lxc/<vmid>` cgroup / virtio guest-agent port, honouring `HOST_PROC`/`HOST_SYS`) surface a "running inside Proxmox" hint via `GET /connectors`. Connector rows replicate via Raft (`CmdConnectorUpsert/Delete`) with the token secret AES-GCM-encrypted under the cluster-shared `JWT_SECRET`. The UI nests guest rows inside the hypervisor's machine card / stats page. See [docs/PROXMOX.md](docs/PROXMOX.md). The same registry hosts the **Pexels wallpaper connector** (`internal/platform/wallpaper`): the API key is stored encrypted, browsers fetch the rotating background through the authenticated `GET /wallpaper` proxy (one cached upstream search per hour serves all clients; dark mode requests black-toned photos via Pexels' `color` filter; originals are capped at `w=3840`).

**Proxmox Backup Server connector** (`internal/metrics/pbs`, type `pbs`). PBS exposes a PVE-style API on `:8007` (token auth uses `PBSAPIToken=<id>:<secret>` — note the `:`), reachable whether PBS runs as a Proxmox guest or on a standalone machine. The leader-gated poller (10 s, mirrors the Proxmox one) upserts the PBS node as a host row and feeds CPU/mem/disk(root fs) through the normal replicated metric pipeline (so it appears as a regular machine card). PBS-specific detail — per-datastore capacity (`/status/datastore-usage`), backed-up machines + last-backup time + last-backup size + machine name (backup groups from `/admin/datastore/{store}/groups`, each sized/named by its newest snapshot via `/admin/datastore/{store}/snapshots` — the snapshot's notes first line, which PVE fills with the guest name by default, falls back to the bare VMID), and backup/verify/GC health (`/nodes/{node}/tasks`) — is kept as an in-memory snapshot on the polling node and **replicated alongside the node's metrics over the off-Raft metric stream** (the batch's `pbs` field), so non-polling cluster peers and a bridged hub cluster also serve `GET /pbs?host_id=` (received snapshots land in the poller's `remoteStatus`, keyed by the local host id resolved via the new `host_external_id` on the metric batch — connector nodes like PBS have no NIC MAC, so the hub regenerates the synthetic MAC and resolution must fall back to the stable external_id). Served to the `frontend/src/widgets/pbs` widget on the PBS stats page (and a compact per-machine "last backup time + size" summary on the PBS machine card). Connector rows replicate via Raft like the Proxmox ones. (PBS exposes no NIC MAC/UUID, so a PBS that is also a Proxmox guest currently shows as its own row rather than linking to the PVE-discovered guest.)

### Environment variables
| Variable | Default | Description |
|----------|---------|-------------|
| `ADDR` | `:8080` | Server listen address |
| `GIN_MODE` | `release` | `debug` / `release` |
| `DEBUG` | `false` | Debug-level logging |
| `DB_TYPE` | `sqlite` | `sqlite` / `postgres` |
| `DB_DSN` | `stats.db` | SQLite path or PostgreSQL DSN |
| `JWT_SECRET` | — | **Required** |
| `REFRESH_SECRET` | — | **Required** |
| `METRICS_RETENTION_DAYS` | `30` | History retention |
| `COOKIE_SECURE` | `false` | Secure flag on auth cookies |
| `ALLOW_ORIGIN` | `*` | CORS origin |
| `HOST_PROC` | `/proc` | Host `/proc` path (Docker deployments; gopsutil reads from env) |
| `HOST_SYS` | `/sys` | Host `/sys` path (Docker deployments) |
| `HOST_ETC` | `/etc` | Host `/etc` (optional; used to read `hostname` for display when bind-mounted) |
| `HOST_ROOT` | — | Host root bind-mount path (e.g. `/host`); disk primary totals use this before `/` |
| `NODE_STATS_HOSTNAME` | — | Optional; when set, collector uses it and API adds `display_name` (overrides card/breadcrumb label). When unset, UI uses registered `name` from the host row. |
| `NODE_STATS_IPV4` | — | Optional override for registered IPv4; omit for auto-detect. |
| `NODE_STATS_PROXMOX_URL` | — | Proxmox API base URL (e.g. `https://10.0.0.2:8006`). With the token vars below set, the node **auto-creates a Proxmox connector on first boot** (`connectors.BootstrapProxmoxFromEnv`) — idempotent (skips if one exists), best-effort (retries, then leaves it to the UI). Set by the Proxmox-LXC installer (`scripts/proxmox-lxc.sh`). Needs a stable `JWT_SECRET` (the token is AES-GCM-encrypted under it). |
| `NODE_STATS_PROXMOX_TOKEN_ID` | — | Proxmox API token id (`user@realm!tokenid`) for the auto-connect above. |
| `NODE_STATS_PROXMOX_TOKEN_SECRET` | — | Proxmox API token secret for the auto-connect above. |
| `NODE_STATS_PROXMOX_SKIP_TLS_VERIFY` | `true` | Skip TLS verification for the auto-connect (PVE ships a self-signed cert). |
| `PROXMOX_AUTOCONNECT` | `true` | Master switch for the Proxmox auto-connect; `0`/`false`/`off` disables it even when the vars above are set. |
| `TRAEFIK_DYNAMIC_DIR` | — | Colon-separated Traefik file-provider dynamic-config dir(s) to derive per-service public URLs for the Applications view. Unset → probes well-known defaults (incl. dokploy's `/etc/dokploy/traefik/dynamic`). Must be bind-mounted into the container to be readable. |
| `NGINX_DYNAMIC_DIR` | — | Colon-separated nginx / Nginx Proxy Manager config dir(s), parsed the same way as `TRAEFIK_DYNAMIC_DIR` (NPM's generated `proxy_host/*.conf`, generic nginx `conf.d`/`sites-enabled` `server`/`proxy_pass` blocks → domains attached to apps). Unset → probes well-known defaults (NPM `/data/nginx/proxy_host`, `/etc/nginx/conf.d`, …) and auto-discovers a running nginx/NPM container's config dir via its mounts. NPM "forward to IP:port" upstreams attach to the app publishing that host port. Diagnose via `GET /docker/traefik-discovery` (`nginx_*` fields). Must be bind-mounted into the container to be readable. |
| `AUTO_UPDATE` | `false` | Check GitHub Releases and apply updates (docker → controller pull+recreate; native → self-replace). Persisted to `.env.agent` by the in-app toggle. |
| `NODE_STATS_RELEASE_CHANNEL` | `stable` | Release line the updater follows: `stable` (latest full release / `:latest` image) or `beta` (newest prerelease release / `:beta` image — docker beta tracks main HEAD's rolling image, native beta self-updates from the latest prerelease asset). Switchable in-app (admin → update popup → channel), persisted to the runtime `.env`/`.env.agent`; honoured by the running server **and** the `node-stats update` CLI. |
| `NODE_STATS_CHANNEL` | `stable` | **Installer-only** knob (`install.sh`, `install.ps1`, `agent-join.sh`, `proxmox-lxc.sh`): pick the channel at install time. Beta defaults the docker image to `:beta` / pulls the latest prerelease `.deb`/native asset, and seeds `NODE_STATS_RELEASE_CHANNEL=beta` into the install's env so the running app + self-updater keep following it. |
| `NODE_STATS_REPO` | `LastSkywalkerER/node-page` | GitHub `owner/name` polled for releases. |
| `NODE_STATS_DATA_DIR` | `/app/data` | Shared data dir holding `desired-state.json` / `controller-status.json` (app↔controller). |
| `NODE_STATS_ENV_FILE` | `./.env` | Runtime `.env` location (read + wizard/bridge writes). Dokploy-style orchestrators re-clone the compose project on redeploy, wiping relative bind mounts — their compose variants point this at `/app/data/.env` inside the persistent named volume. |
| `NODE_STATS_MANAGED_EXTERNALLY` | `false` | When true (or `TRAEFIK_DYNAMIC_DIR` set, or `/etc/dokploy` present), disables controller compose mutation — the orchestrator owns the lifecycle. |
| `NODE_STATS_DEPLOY_WEBHOOK_URL` | — | Orchestrator deploy-webhook URL (dokploy: Deployments tab). On managed-externally deployments "update now"/auto-update trigger it (POST, GET fallback) so the orchestrator pulls the latest image and redeploys. Settable from the update popup (admin); the auto loop applies any target at most once (persisted in `webhook-update.json`) so a no-op redeploy/recreate can't reboot-loop. |
| `NODE_STATS_APP_PREFIX_GROUPING` | `true` | Applications view fallback: merge apps sharing a common dash/underscore name prefix into one (e.g. Dokploy's `node-stats-app-…`/`-db-…`/`-compose-…` → `node-stats`). Set `false`/`0`/`off` to disable. No-op for distinctly-named projects. |

**Controller sidecar** (Docker only; same image run as `node-stats controller`). Owns the docker socket and applies the compose stack the app requests:
| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_STATS_STACK_HOST_DIR` | — | Host path of the compose project dir, identity-mounted at the same path so the docker CLI's relative bind paths resolve to real host paths. |
| `NODE_STATS_STACK_DIR` | `/app/stack` | Compose project dir as seen by the controller (= `NODE_STATS_STACK_HOST_DIR`). |
| `NODE_STATS_APP_SERVICE` | `node-stats` | Compose service the controller recreates. |
| `NODE_STATS_PROJECT` | `node-stats` | Compose `-p` project name (shared with the installer). |

**Raft cluster sync** (optional; see `docker-compose.cluster.yml`). When unset the node runs standalone:
| Variable | Default | Description |
|----------|---------|-------------|
| `RAFT_ENABLED` | `false` | Enable the Raft consensus layer |
| `RAFT_CLUSTER_ID` | — | Logical cluster identifier shared by all peers of one site — but **unique across sites** (the uplink hub rejects batches carrying its own id; the wizard generates `<host>-<rand>`) |
| `RAFT_NODE_ID` | — | Unique node id within the cluster |
| `RAFT_BIND_ADDR` | — | Local Raft TCP listen address (e.g. `:7000`) |
| `RAFT_ADVERTISE_ADDR` | — | Address peers use to reach this node's Raft port |
| `RAFT_ADVERTISE_PUBLIC_URL` | — | HTTP base URL peers use for join/forward |
| `RAFT_DATA_DIR` | — | Directory for Raft log/snapshot storage |
| `RAFT_BOOTSTRAP` | `false` | Bootstrap this node as the initial leader (first node only) |
| `RAFT_BRIDGE_MODE` | `both` | Bridge direction: `push` (spoke — uplink hosts+metrics to a hub, receive nothing), `receive` (hub — accept uplinks from many spokes, ship nothing), `both` (legacy symmetric pair) |
| `RAFT_BRIDGE_ENABLED` / `RAFT_BRIDGE_SHARED_SECRET` / `RAFT_BRIDGE_REMOTE_SEEDS` | — | Cross-cluster bridge: HMAC secret shared by all sites; seeds = hub URL(s) the spoke POSTs to (outbound-only) |

The setup-wizard "Join an existing cluster" flow writes the resolved `RAFT_*` values to the node's `.env` so the configuration survives restarts/rebuilds.

---

## Distribution & self-update

**Native single binary.** SQLite is pure-Go (`github.com/glebarez/sqlite` → `modernc.org/sqlite`), so the whole app builds with `CGO_ENABLED=0`. The frontend is embedded via `go:embed` behind the `embed_dist` build tag (`internal/webui/embed.go`; `noembed.go` is the dev stub that falls back to on-disk `internal/webui/dist`). Vite outputs to `internal/webui/dist`. Build metadata is injected with `-ldflags -X system-stats/internal/version.{Version,Commit,Date}`.

- **`scripts/build`** — frontend build → `go build -tags embed_dist`.
- **`.github/workflows/release.yml`** — on `v*` tags, cross-compiles `linux/{amd64,arm64}`, `darwin/arm64`, `windows/amd64` from one runner, packages tar.gz/zip + `SHA256SUMS`, attaches to a GitHub Release.
- **`.github/workflows/docker.yml`** — multi-arch (`linux/amd64,arm64`) image + semver tags on `v*` (alongside `latest` on main). Image: `ghcr.io/lastskywalkerer/node-page`.

**Subcommands** (single binary): no args → HTTP server; `controller` → compose-applying sidecar; `gen-compose` → emit the canonical base compose; `update [--check]` → native self-update.

**One-line installers** (`scripts/install.sh` curl|bash for Linux/macOS, `scripts/install.ps1` irm|iex for Windows + Docker Desktop). **`scripts/agent-join.sh`** wraps install.sh into a one-command *install + cluster join*: the leader's admin panel (Nodes → Add a node) generates `NODE_STATS_JOIN_URL=… NODE_STATS_JOIN_KEY=… bash -c "$(curl -fsSL …/agent-join.sh)"` next to the one-shot token — the script installs the stack, posts `/setup/join-raft-cluster` with auto-derived addresses, and waits for the snapshot. They detect OS/arch, check docker+compose, create a stack dir, write `.env.agent` **as a file** (the bind-mount-dir pitfall), generate the base compose via `docker run <image> gen-compose`, write an OS host-caps `docker-compose.override.yml` (from `install/`), and `docker compose up -d`. On a fresh install the script **auto-picks free ports**: if the default HTTP `9090` / Raft `7000` is already taken it scans upward for the next free one (`ss` → `/proc/net/tcp` → loopback-connect probes), while an explicitly env-pinned port that's busy fails loudly; the picked ports land in the stack `.env`, compose passes `NODE_STATS_RAFT_PORT` into the container so wizard bind/advertise defaults follow it, and `agent-join.sh` reads the actual ports back from the stack `.env`. Re-running install against an **existing stack** first health-checks it (app-container state + `GET /api/v1/health`): healthy → in-place refresh; broken (e.g. a half-finished install whose `.env` pins a port another service owns) → prints a diagnosis and asks before recreating *only* the node-stats containers (prompt via `/dev/tty` since stdin holds the piped script; `NODE_STATS_REPAIR=1` answers yes non-interactively) — data/secrets/settings are kept and clashing ports re-picked, except a cluster-joined node's Raft port, which is never moved silently. Subcommands `install|update|uninstall`. **Reverse proxy** (NPM/Traefik) in front of the app: wiring goes into the installer-owned `docker-compose.override.yml` (shared docker network → target the in-container `node-stats:9090`; via host → the published `NODE_STATS_PORT` from `.env`); SSE already sends `X-Accel-Buffering: no`. See [docs/REVERSE-PROXY.md](docs/REVERSE-PROXY.md).

**Wizard-driven compose mutation + controller.** DB/topology are *not* baked into compose ahead of time. The app writes a **desired-state descriptor** (`internal/platform/setup` `BuildComposeContent`/`DesiredState`) to `NODE_STATS_DATA_DIR/desired-state.json`; the **controller sidecar** (`internal/platform/controller`) polls it, regenerates `docker-compose.yml`, and runs `docker compose up -d --no-deps --force-recreate node-stats` (managed Postgres first `up -d --wait db`). It recreates only the app so the controller survives, and writes `controller-status.json`. DB modes: `sqlite` (file in `/app/data`), `postgres-managed` (injects a `postgres:16-alpine` `db` service + `pgdata` volume), `postgres-external`. `DB_TYPE`/`DB_DSN` live in the controller-managed app `environment:`; `JWT_SECRET`/`REFRESH_SECRET` stay in `.env.agent` (loaded via godotenv). Host caps live in the installer-owned `docker-compose.override.yml`, which the controller never touches.

- **DB choice is first-run-only** (no sqlite↔postgres migration). A DB-engine switch in the wizard returns `restart_pending` and **defers admin creation**: the controller recreates the app on the new (empty) DB, then the frontend re-submits `/setup/complete` so the admin lands on the new DB (two-phase). When `ManagedExternally()` is true the controller is disabled and the wizard tells the operator to edit compose manually.

**Auto-update** (`internal/platform/update`). Polls GitHub Releases (cached, 6 h), semver-compares against the build version (non-semver dev/main builds never claim an update), and exposes state via `GET /version`. The admin toggle persists `AUTO_UPDATE` to `.env.agent`; "update now" / the auto loop apply the latest — **docker** bumps `desired-state.json` with `pull_before_apply` + the channel image (the controller syncs `NODE_STATS_IMAGE` into the stack `.env` so a pinned value can't shadow it, then pulls + recreates), **native** downloads the matching release asset, verifies it against `SHA256SUMS`, and self-replaces the binary (`node-stats update`). **Managed-externally** deployments (dokploy, …) update through the orchestrator's own deploy webhook when `NODE_STATS_DEPLOY_WEBHOOK_URL` is configured (admin update popup → "Deploy webhook URL"); the auto loop never re-fires the webhook for the same release tag.

**Release channels (`NODE_STATS_RELEASE_CHANNEL`: `stable` | `beta`)** apply across *every* install type. **stable** = the latest full release (`:latest` image / `/releases/latest` asset). **beta**: for **docker** it's the rolling `:beta` image rebuilt from `main` HEAD — "newer beta" means `main` moved past this build's commit (commit-compared, `Latest = main@<sha>`); for **native/Proxmox-LXC/macOS** there is no rolling binary, so beta follows the newest **prerelease release tag** and self-updates from its asset (prerelease-aware semver ordering, so `beta.19 → beta.20` rolls forward but a re-listed older prerelease doesn't). The channel is switchable in-app for any deployment and persisted to the runtime env; the `node-stats update` CLI loads that env so a native self-update (systemd `update-native`, `proxmox-lxc.sh update`, cron) follows the same line instead of always pulling stable. A fresh install can start on beta via the installer's `NODE_STATS_CHANNEL=beta`. **Managed-externally** is the one exception: the orchestrator owns the image tag, so beta there means pinning the `:beta` image in its compose — the deploy webhook just redeploys whatever tag is pinned.

---

## Frontend

### Tech stack
- **React 19**, TypeScript 5.9, Vite 6
- **Tailwind CSS v4** — CSS-first config, no `tailwind.config.ts`, plugin via `@tailwindcss/vite`
- **shadcn/ui v4** with **Base UI** (`@base-ui/react`) primitives — **no `@radix-ui/*`**
- **React Router v7**, Tanstack Query v5, Zustand v5, Recharts 2, date-fns v4
- **Forms**: Zod + react-hook-form + @hookform/resolvers

### Directory structure
```
frontend/src/
├── App.tsx                        # Routes + ProtectedLayout
├── main.tsx
├── index.css                      # Tailwind imports + CSS vars (oklch) + dark/light themes
├── lib/
│   └── utils.ts                   # cn() from shadcn
├── components/
│   └── ui/                        # shadcn components (button, card, chart, badge, ...)
├── pages/
│   ├── AuthPage.tsx
│   ├── SetupPage.tsx
│   ├── MachineListPage.tsx        # /machines
│   ├── MachineStatsPage.tsx       # /machines/:id/stats
│   └── MachineContainersPage.tsx  # /machines/:id/containers
├── widgets/
│   ├── auth/                      # LoginWidget, RegisterWidget + hooks + schemas
│   ├── setup/                     # Wizard step widgets
│   ├── cpu/                       # CPUWidget, useCPU, schemas
│   ├── memory/                    # MemoryWidget, useMemory, schemas
│   ├── disk/                      # DiskWidget, useDisk, schemas
│   ├── network/                   # NetworkWidget, useNetwork, schemas
│   ├── sensors/                   # SensorsWidget, useSensors, schemas
│   ├── docker/                    # DockerWidget, useDocker, schemas
│   ├── hosts/                     # useHosts, schemas
│   └── connection-status/         # ConnectionStatusWidget, useConnectionStatus
└── shared/
    ├── components/
    │   ├── AppHeader.tsx           # Logo + breadcrumb/tabs + theme toggle + logout
    │   ├── ErrorBoundary.tsx       # Per-widget crash isolation
    │   └── MetricCardSkeleton.tsx  # Loading placeholder
    ├── guards/
    │   ├── ProtectedRoute.tsx      # Redirects to /auth if not logged in
    │   └── SetupRoute.tsx          # Redirects to /setup if not configured
    ├── hooks/
    │   ├── useTheme.ts             # dark/light toggle; initTheme() called in App
    │   └── useEventSource.ts       # SSE connection hook
    ├── lib/
    │   ├── api.ts                  # Axios instance (baseURL /api/v1)
    │   ├── auth.ts                 # login/logout/refresh helpers
    │   ├── chartColors.ts          # CHART_COLORS constants (hex)
    │   ├── metricsStore.ts         # Zustand store fed by SSE
    │   └── utils.ts                # formatBytes, getContainerStateColor, cn
    ├── store/
    │   └── user.ts                 # Zustand auth store (user, token, clearAuth)
    ├── types/
    │   └── metrics.ts              # TypeScript types for all metrics
    └── ui/
        ├── password-input.tsx      # Custom password field
        └── select.tsx              # Native <select> wrapper for react-hook-form
```

### Routing
```
/setup                             → SetupPage (public, no guards)
/auth                              → AuthPage (SetupRoute guard only)
/                                  → ProtectedLayout (SetupRoute + ProtectedRoute)
  /machines                        → MachineListPage
  /machines/:id/stats              → MachineStatsPage   (mounts SSE)
  /machines/:id/containers         → MachineContainersPage (mounts SSE)
* → redirect to /machines or /auth
```
`ProtectedLayout` = `SetupRoute` → `ProtectedRoute` → `<AppHeader /> + <Outlet />`.

### Theming
- **2 modes only**: `dark` / `light` via `.dark` class on `<html>`
- Toggle + localStorage persistence: `useTheme()` hook in `shared/hooks/useTheme.ts`
- Call `initTheme()` once on app mount (in `App.tsx` useEffect)
- All colors as `oklch()` CSS vars in `index.css` — `:root` (light) and `.dark` blocks
- **No hardcoded `#hex` colors in CSS** — only in `chartColors.ts` for Recharts

### shadcn/ui components
Located in `src/components/ui/`. Add new ones with:
```bash
npx shadcn@latest add @shadcn/<component>
```
MCP server configured in `frontend/.mcp.json` — use `mcp__shadcn__*` tools to browse/add components without running CLI manually.

**Available components**: `alert`, `badge`, `button`, `card`, `chart`, `dropdown-menu`, `form-field`, `input`, `label`, `progress`, `scroll-area`, `select`, `separator`, `skeleton`, `tabs`, `tooltip`.

### Widget pattern
```tsx
// 1. Fetch historical data
const { data: metrics, isLoading } = useXxx(hostId)

// 2. Read live data from SSE store
const live = useMetricsStore(s => s.xxx)

// 3. Render with Card + shadcn primitives
// 4. Charts via ChartContainer (see Charts section)
```
Each widget in page components is wrapped in `<ErrorBoundary name="...">` to isolate crashes.

### Machine stats data flow (SSE-first)
- **REST** (`GET /cpu|memory|disk|network|docker?host_id=`): one load per visit — `latest` from DB + `history` for charts (`staleTime: Infinity`, no `refetchInterval`).
- **Node cards** load each host as one unit: `HostCard` holds a full-card skeleton until its metric/health/PBS queries have all loaded once, so the card reveals whole instead of piecemeal. Its **static hardware identity** (cpu model/cores, ram/disk totals) rides on the host row: `GET /hosts` enriches each host from its latest metric rows (`hosts.StaticHardwareSource`, adapted over the cpu/memory/disk repos in DI), so the card's static fields come from that one request rather than the per-metric queries / SSE. The CPU model is persisted on the cpu metric row (`HistoricalCPUMetric.ModelName`) so it's queryable at read time, not SSE-only.
- **SSE** (`GET /stream?host_id=`): each collector tick pushes a live snapshot with `collecting_host_id`; `useLiveMetricsQuerySync` merges it into the same React Query keys so widgets update without polling.
- **Sensors**: not in SSE; single REST load per page (`/sensors?host_id=`).
- **Health** (machine cards): poll every 5s. **`status: online`** only if `last_seen` is fresh (single threshold, no agent distinction). UI uses `status`, not HTTP success. Cards use JSON **`uptime`** (this API process uptime). Card stripe/icon: green online, **red offline**.
- **Cluster sync (Raft)**: clusters are formed through the setup wizard. The leader issues a one-shot **connect key**; a fresh node enters **"Join an existing cluster"** in its wizard, supplies that key plus the cluster node URL, and posts to **`POST /setup/join-raft-cluster`**. The leader adds it as a **voter** via the Raft consensus layer (`/api/v1/raft/*`), and application state replicates across all peers. The joiner polls **`GET /setup/raft-progress`** while the leader catches it up. `RAFT_*` env vars (see env table / `docker-compose.cluster.yml`) configure node id, bind/advertise addresses, data dir, and bootstrap. **`GET /raft/status`** exposes the underlying `hashicorp/raft` stats for diagnostics.
- **Local collector host**: Metrics from **this** process always use **`hosts.id = 1`** (`LocalCollectorHostID`). **`UpsertLocalHost`** updates that row on every register/get-current; hostname/MAC may change (e.g. Docker) without creating new rows. **`GetAllHosts`** orders local collector first.
- Use `useXxx(..., { mode: 'poll' })` only if you need legacy interval refetch without a stream.

### Charts
Recharts wrapped in shadcn `ChartContainer` from `@/components/ui/chart`.

```tsx
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { CHART_COLORS } from '@/shared/lib/chartColors'

// Define config — can be dynamic (inside component) for color-coded alerts
const chartConfig: ChartConfig = {
  usage: { label: 'CPU %', color: CHART_COLORS.cpu }, // or dynamic color
}

<ChartContainer config={chartConfig} className="h-20 w-full">
  <AreaChart data={data} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
    <defs>
      <linearGradient id="grad" x1="0" y1="0" x2="0" y2="1">
        <stop offset="5%"  stopColor="var(--color-usage)" stopOpacity={0.25} />
        <stop offset="95%" stopColor="var(--color-usage)" stopOpacity={0} />
      </linearGradient>
    </defs>
    <XAxis dataKey="time" axisLine={false} tickLine={false} tick={{ fontSize: 9 }} />
    <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 9 }} width={24} />
    <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel />} />
    <Area dataKey="usage" stroke="var(--color-usage)" fill="url(#grad)" strokeWidth={1.5} dot={false} />
  </AreaChart>
</ChartContainer>
```
- `ChartContainer` includes `ResponsiveContainer` — do **not** wrap in one manually
- Axis tick colors handled automatically via CSS selectors — no `fill: 'currentColor'` needed
- Use `var(--color-<key>)` for stroke/fill — set by `ChartStyle` from the config object

### date-fns v4 gotcha
v4 throws `RangeError: Invalid time value` on invalid dates — always guard:
```ts
const d = new Date(p.timestamp)
return isNaN(d.getTime()) ? '' : format(d, 'HH:mm')
```

### sensors module
Only returns data on Linux. Returns empty array on macOS/Windows — handle gracefully in UI.
