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
Existing modules: `cpu`, `memory`, `disk`, `network`, `docker`, `sensors`, `hosts`, `users`, `history_metrics`, `setup`, `health`, `system`, `stream`.

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
GET  /setup/status          # includes machine_hints (suggested hostname / IPv4) while setup_needed
GET  /setup/config
POST /setup/preview-env
POST /setup/complete
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
```
All metric endpoints accept `?hours=<float>` (default `0.0833` ≈ 5 min) and `?host_id=<uint>`. **`host_id=0` means this server instance** (resolved via current host MAC). Latest and history are always scoped to that host row; unknown `host_id` returns empty payloads (`latest: null`, empty history). **Metrics are replicated cluster-wide via Raft (`CmdMetricBatch`)**, so any node serves any host's CPU/mem/disk/net/docker history (and a host's data survives it going offline). The frontend is uniform: one REST load per metric on mount, then a **single SSE stream for every host** — the node publishes its own host's metrics each cycle *and* every replicated peer's metrics (`applyMetricBatch` → `broker.Publish`), and the client keeps events whose `collecting_host_id` matches the viewed host. The browser only ever talks to its own node; nodes sync over Raft. **Sensors are not replicated** (`/sensors` returns empty for remote hosts). See [docs/CLUSTER.md](docs/CLUSTER.md) for the full data-flow.

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
| `TRAEFIK_DYNAMIC_DIR` | — | Colon-separated Traefik file-provider dynamic-config dir(s) to derive per-service public URLs for the Applications view. Unset → probes well-known defaults (incl. dokploy's `/etc/dokploy/traefik/dynamic`). Must be bind-mounted into the container to be readable. |

**Raft cluster sync** (optional; see `docker-compose.cluster.yml`). When unset the node runs standalone:
| Variable | Default | Description |
|----------|---------|-------------|
| `RAFT_ENABLED` | `false` | Enable the Raft consensus layer |
| `RAFT_CLUSTER_ID` | — | Logical cluster identifier shared by all peers |
| `RAFT_NODE_ID` | — | Unique node id within the cluster |
| `RAFT_BIND_ADDR` | — | Local Raft TCP listen address (e.g. `:7000`) |
| `RAFT_ADVERTISE_ADDR` | — | Address peers use to reach this node's Raft port |
| `RAFT_ADVERTISE_PUBLIC_URL` | — | HTTP base URL peers use for join/forward |
| `RAFT_DATA_DIR` | — | Directory for Raft log/snapshot storage |
| `RAFT_BOOTSTRAP` | `false` | Bootstrap this node as the initial leader (first node only) |

The setup-wizard "Join an existing cluster" flow writes the resolved `RAFT_*` values to the node's `.env` so the configuration survives restarts/rebuilds.

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
