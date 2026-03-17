# node-stats — Architecture Reference for Agents

Self-hosted system monitoring app. Go backend (Gin, GORM, gopsutil) + React frontend (Vite, React Query, Zustand, Tailwind). Collects CPU, memory, disk, network, Docker metrics, stores history in SQLite or PostgreSQL, serves via REST API + SSE stream.

---

## Module structure (repeated pattern)

```
internal/modules/{name}/
├── presentation/        # Gin HTTP handlers
├── application/         # Service interface + implementation, business logic
└── infrastructure/
    ├── collectors/      # Data collection (gopsutil, Docker API)
    ├── entities/        # GORM models
    └── repositories/    # Repository interface + GORM implementation
```

Existing modules: `cpu`, `memory`, `disk`, `network`, `docker`, `sensors`, `hosts`, `users`, `history_metrics`, `setup`, `health`, `system`.

---

## Key architectural rules

1. **New module = new directory following the pattern above.** No business logic in handlers.
2. **Handlers depend only on the Service interface**, never directly on a repository.
3. **All dependencies are created in `internal/app/di/container.go`** and passed via constructors.
4. **Migrations only in `internal/app/database/migrations.go`** — never in repository constructors.
5. **Routes only in `internal/app/server/server.go`**.
6. **Dialect-agnostic time queries** — use `database.TimeOffsetQuery(db, hours)` and `database.TimeOffsetQueryWithHost(db, hostId, hours)` from `internal/app/database/dialect.go` for SQLite/PostgreSQL compatibility.
7. **Auth**: tokens in HttpOnly cookies (planned). Middleware `AuthJWT` in `internal/app/middleware/auth.go` — reads from cookie, falls back to `Authorization` header.
8. **Rate limiting** on auth endpoints via `middleware.RateLimitMiddleware` from `internal/app/middleware/ratelimit.go` (to be added in Phase 2).
9. **Real-time updates via SSE** at `/api/v1/stream`. Broker in `internal/app/stream/broker.go` (to be added in Phase 3).
10. **Data retention**: goroutine in `internal/app/retention/service.go`, configured via `METRICS_RETENTION_DAYS`.

---

## Existing repository interfaces (use for mocks in tests)

- `cpu/infrastructure/repositories.CPURepository`
- `memory/infrastructure/repositories.MemoryRepository`
- `disk/infrastructure/repositories.DiskRepository`
- `network/infrastructure/repositories.NetworkRepository`
- `docker/domain/repositories.DockerRepository`
- `hosts/infrastructure/repositories.HostRepository`
- `users/infrastructure/repositories.UserRepository`
- `users/infrastructure/repositories.RefreshTokenRepository`

---

## Configuration (env vars)

| Variable | Default | Description |
|---|---|---|
| `ADDR` | `:8080` | Server listen address |
| `GIN_MODE` | `release` | `debug` / `release` |
| `DEBUG` | `false` | Enable debug-level logging |
| `DB_TYPE` | `sqlite` | `sqlite` / `postgres` |
| `DB_DSN` | `stats.db` | SQLite file path or PostgreSQL DSN |
| `JWT_SECRET` | — | **Required** — access token signing key |
| `REFRESH_SECRET` | — | **Required** — refresh token signing key |
| `METRICS_RETENTION_DAYS` | `30` | Days to keep historical metrics |
| `COOKIE_SECURE` | `false` | Set Secure flag on auth cookies (enable in prod with TLS) |
| `ALLOW_ORIGIN` | `*` | CORS allowed origin |
| `HOST_PROC` | `/proc` | Path to host `/proc` (for Docker deployments) |
| `HOST_SYS` | `/sys` | Path to host `/sys` (for Docker deployments) |

---

## API routes

All routes are prefixed with `/api/v1`.

**Public (no auth):**
- `GET /health` — health check (used by k8s probes / load balancers)
- `GET /setup/status`, `GET /setup/config`, `POST /setup/complete` — first-run wizard
- `POST /auth/register`, `POST /auth/login`, `POST /auth/refresh`

**Protected (requires `AuthJWT` middleware):**
- `POST /auth/logout`
- `GET /users/me`, `GET /users`, `PATCH /users/:id`, `DELETE /users/:id`
- `GET /metrics/current` — aggregated snapshot
- `GET /cpu`, `GET /memory`, `GET /disk`, `GET /network`, `GET /docker`, `GET /sensors`
- `GET /hosts`, `GET /hosts/current`, `POST /hosts/register`
- `GET /stream` — SSE stream (planned)

All metric endpoints accept `?hours=<float>` (default `0.0833` ≈ 5 min) and `?host_id=<uint>`.

---

## Frontend structure

```
frontend/src/
├── pages/           # Route components (Dashboard, AuthPage, SetupPage)
├── widgets/         # Feature widgets (cpu/, memory/, docker/, ...)
└── shared/
    ├── components/  # Reusable UI + ErrorBoundary
    ├── guards/      # ProtectedRoute, SetupRoute
    ├── hooks/       # useEventSource (SSE), useTheme
    ├── lib/         # api.ts (axios), auth.ts, metricsStore.ts
    ├── store/       # user.ts (Zustand — auth state)
    ├── types/       # metrics.ts — TypeScript types
    ├── themes/      # glass-aurora, neon-terminal, cards-flow, slate-pro
    └── ui/          # Radix UI component wrappers
```

**Widget pattern**: component reads live data from `useMetricsStore` (SSE) + historical data via `useQuery` without `refetchInterval`.

**Form validation**: Zod + react-hook-form + @hookform/resolvers only.

**Charts**: Recharts only.

---

## Important implementation notes

- `history_metrics/core/service.go` — orchestrates periodic collection across all modules every 5 seconds and saves via each module's service
- `internal/app/database/dialect.go` — `TimeOffsetQuery`/`TimeOffsetQueryWithHost` detect dialector name (`postgres` vs `sqlite`) and return appropriate WHERE clause
- `internal/app/retention/service.go` — runs cleanup immediately on start, then every hour
- Token hashing in `users/application/token_service.go` uses SHA-256 (not bcrypt) for refresh tokens
- `sensors` module only works on Linux; returns empty on other platforms
