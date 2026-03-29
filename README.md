# Node Stats

**Node Stats** is an open-source, self-hosted system monitoring and infrastructure monitoring dashboard built with Go and React. Monitor your servers, Docker containers, and system resources in real-time with a beautiful, modern web interface. Perfect for DevOps engineers, system administrators, and developers who need comprehensive server monitoring, container monitoring, and infrastructure metrics visualization.

## What is Node Stats?

Node Stats is a lightweight, self-hosted alternative to commercial monitoring solutions like Grafana, Prometheus, or Datadog. It provides real-time system metrics collection, Docker container monitoring, and a beautiful dashboard interface without the complexity of enterprise monitoring tools. Built with Go for performance and React for a modern user experience, Node Stats is ideal for monitoring single servers, development environments, or small infrastructure deployments.

### Key Capabilities

- **Server Monitoring**: Real-time CPU, memory, disk, and network metrics
- **Docker Monitoring**: Container statistics, resource usage, and health monitoring
- **System Metrics**: Temperature sensors, system information, and hardware monitoring
- **Self-Hosted**: Complete control over your data with no external dependencies
- **Lightweight**: Minimal resource footprint compared to enterprise monitoring solutions
- **Modern UI**: Beautiful, customizable dashboard themes with real-time updates

## Features

- **Real-time Monitoring**: Live CPU usage, memory usage, disk I/O, network traffic, and Docker container metrics
- **Beautiful UI**: Modern monitoring dashboard with multiple customizable themes (Neon Terminal, Glass Aurora, Cards Flow, Slate Pro)
- **RESTful API**: Clean REST API endpoints for all system metrics and Docker statistics
- **Docker Support**: Comprehensive Docker container monitoring with resource usage tracking
- **Persistent Storage**: SQLite database for historical metrics and data retention
- **User Authentication**: Secure login system with JWT tokens and refresh tokens
- **Temperature Monitoring**: Hardware temperature sensor monitoring for system health
- **Multi-theme Support**: Switch between different dashboard themes for personalized monitoring experience

## Use Cases

Node Stats is perfect for:

- **Development Teams**: Monitor development servers and staging environments
- **DevOps Engineers**: Track infrastructure health and resource utilization
- **System Administrators**: Monitor server performance and system metrics
- **Docker Users**: Monitor containerized applications and Docker host resources
- **Home Lab Enthusiasts**: Self-hosted monitoring for personal servers and homelabs
- **Small to Medium Infrastructure**: Lightweight monitoring solution without enterprise complexity
- **Privacy-Conscious Users**: Self-hosted alternative to cloud-based monitoring services

## Roadmap

- ✅ Real-time metrics monitoring (CPU, memory, disk, network, Docker containers, temperature sensors)
- ✅ Modern UI dashboard with multiple themes (Neon Terminal, Glass Aurora, Cards Flow, Slate Pro)
- ✅ RESTful API endpoints for all metrics
- ✅ User authentication (registration, login, refresh tokens, user management)
- ✅ SQLite database for historical metrics storage
- ✅ Application configuration via UI on first launch (setup wizard for initial configuration)
- ✅ Prometheus metrics export (`/api/v1/metrics`, enabled via `PROMETHEUS_ENABLED=true`)
- ✅ Swagger UI for API documentation (`/swagger/`)
- ✅ PostgreSQL database support (alternative to SQLite via `DB_TYPE=postgres`)
- ✅ Health check endpoint (`/api/v1/health`) for load balancers and Kubernetes probes
- ✅ Live metrics stream via Server-Sent Events (`/api/v1/stream`)
- ✅ Configurable data retention (`METRICS_RETENTION_DAYS` for automatic cleanup of old metrics)
- ✅ Hosts registration and management
- ✅ Admin/user roles
- ✅ Backend unit tests for service layer
- ❌ Multi-node statistics synchronization and aggregation (push-based model where each node sends metrics to a central server, eliminating the need to expose individual nodes to the internet for secure centralized collection)
- ❌ Alert system (configurable notifications when metric thresholds are exceeded)
- ❌ Stack detection with aggregation into apps with icons and minimal stats
- ❌ Auto config for reverse proxy for apps routing
- ❌ Application port tunneling (tunnel selected application ports from closed machines through the central node to local machine for fast, direct, and secure access)
- ❌ Container logs monitoring (real-time viewing and filtering of Docker container logs)
- ❌ Additional time-series databases support (integration with databases better suited for time-series data like InfluxDB, TimescaleDB)

## Screenshots

UI collage (dark glass / neon and light themes). Click an image on GitHub to open the full-size PNG.

<table>
<tr>
<td align="center" valign="top" width="50%">
<a href="assets/sign-in.png"><img src="assets/sign-in.png" width="100%" alt="Sign in"/></a><br/>
<sub><b>Sign in</b> — NODE-STATS logo, email/password fields, primary Sign In action.</sub>
</td>
<td align="center" valign="top" width="50%">
<a href="assets/machines-overview.png"><img src="assets/machines-overview.png" width="100%" alt="Machines overview"/></a><br/>
<sub><b>Machines</b> — fleet cards: hostname, OS, IP/MAC, kernel, last seen, latency, uptime.</sub>
</td>
</tr>
<tr>
<td align="center" valign="top" width="50%">
<a href="assets/live-metrics-dark-theme.png"><img src="assets/live-metrics-dark-theme.png" width="100%" alt="Live metrics dark theme"/></a><br/>
<sub><b>Live metrics (dark)</b> — CPU, memory, disk, network, sensors with sparklines and load.</sub>
</td>
<td align="center" valign="top" width="50%">
<a href="assets/docker-containers.png"><img src="assets/docker-containers.png" width="100%" alt="Docker containers"/></a><br/>
<sub><b>Containers</b> — Docker compose groups, running/total, CPU/memory rings, ports, I/O.</sub>
</td>
</tr>
<tr>
<td align="center" valign="top" width="50%">
<a href="assets/live-metrics-light-theme.png"><img src="assets/live-metrics-light-theme.png" width="100%" alt="Live metrics light theme"/></a><br/>
<sub><b>Live metrics (light)</b> — same metrics layout on a light, patterned dashboard theme.</sub>
</td>
<td align="center" valign="top" width="50%">
<a href="assets/admin-users.png"><img src="assets/admin-users.png" width="100%" alt="Admin users"/></a><br/>
<sub><b>Admin · Users</b> — email invites, one-time links, directory with roles (e.g. Admin).</sub>
</td>
</tr>
<tr>
<td align="center" valign="top" colspan="2">
<a href="assets/admin-nodes.png"><img src="assets/admin-nodes.png" width="50%" alt="Admin nodes"/></a><br/>
<sub><b>Admin · Nodes</b> — generate join links, push URL, registered hosts, token regen &amp; remove.</sub>
</td>
</tr>
</table>

## Installation & Quick Start

Get Node Stats up and running in minutes. Choose between Docker deployment (recommended for production) or local development setup.

### Using Docker (Recommended)

The easiest way to deploy Node Stats is using Docker Compose. This method provides automatic container management and easy configuration.

```yaml
services:
  node-stats:
    image: 'ghcr.io/lastskywalkerer/node-page:latest'
    # ports:
    #   - "8080:8080"
    # Optional: host network mode to access host interface metrics
    network_mode: host
    volumes:
      # Mount database file for persistence
      - ./stats.db:/app/stats.db
      # Mount Docker socket for Docker metrics
      - /var/run/docker.sock:/var/run/docker.sock:ro
      # Mount host filesystem for system metrics
      - /:/host:ro
    pid: host
    ipc: host
    restart: unless-stopped
    environment:
      - ADDR=${ADDR:-:8080}
      - GIN_MODE=release
      - HOST_PROC=/host/proc
      - HOST_SYS=/host/sys
      - HOST_ETC=/host/etc
      - JWT_SECRET=${JWT_SECRET:-your-jwt-secret-key-change-in-production}
      - REFRESH_SECRET=${REFRESH_SECRET:-your-refresh-secret-key-change-in-production}
```

Then run:

```bash
docker-compose up -d
```

The application will be available at `http://localhost:8080` by default. You can change the port by setting the `ADDR` environment variable (e.g., `ADDR=:9090`).

#### Cluster: Docker agent + main on your machine

If **main** runs on the host (e.g. `./scripts/dev` on `:8080`) and the **agent** runs in Docker (`docker compose` on `:9090`), the agent has its **own** SQLite DB. After **Connect** on the agent (paste join link), main returns a **unique push token** once; the agent saves `MAIN_NODE_URL` and `NODE_ACCESS_TOKEN` to its local `.env` (in the container that is often **not** persisted across image rebuilds — use compose `env_file` / env vars for durability).

**On main (admin → Nodes):** expand **Agent URL & token** under each host. You always see the **base URL** and **push URL**. The plaintext token is **not** stored on main (only a hash), so it cannot be “viewed” later — use **Regenerate token** to issue a new one (old token stops working) and copy the `.env` snippet.

**Main env (optional):** `PUBLIC_BASE_URL` — if set, join links and the admin “agent setup” URLs use this instead of the browser `Host` header. Use when agents must call a different host than the UI (e.g. `http://host.docker.internal:8080`).

**Docker agent env:** edit **`.env.agent`** in the repo root (tracked template with empty token). Compose mounts it as **`/app/.env`** in the container, so **Connect** and restarts keep the same file on disk. Copy from **`.env.agent.example`** if you remove the file. Do not commit production tokens.

| Variable | Example | Notes |
|----------|---------|--------|
| `MAIN_NODE_URL` | `http://host.docker.internal:8080` | Must match what main shows in admin (or `PUBLIC_BASE_URL` on main) |
| `NODE_ACCESS_TOKEN` | from Connect or **Regenerate** in admin | `Authorization: Bearer …` on `POST /api/v1/nodes/push` |

If either is missing, the agent collects locally but does not push; the server logs a **one-time warning**.

### Local Development

#### Full Dev Run (Recommended)

The project uses **Overmind** with a **Procfile** to run backend and frontend in parallel. Backend uses **Air** for live reload; frontend uses **Vite** with proxy to the API.

**Prerequisites:**
- [Overmind](https://github.com/DarthSim/overmind) — `brew install overmind` (macOS) or [install from releases](https://github.com/DarthSim/overmind/releases)
- Go, Node.js, Yarn

```bash
# Install backend dependencies
go mod download

# Install frontend dependencies
cd frontend && yarn install && cd ..

# Set required environment variables
export JWT_SECRET=your-jwt-secret-key
export REFRESH_SECRET=your-refresh-secret-key

# Optional: customize server address (default: :8080)
export ADDR=:8080

# Run backend + frontend together
./scripts/dev
```

- **Backend** (Air): `http://localhost:8080` — live reload on Go file changes
- **Frontend** (Vite): `http://localhost:5173` — proxies `/api` and `/ws` to backend

#### Backend Only (Go)

```bash
# Install dependencies
go mod download

# Set required environment variables
export JWT_SECRET=your-jwt-secret-key
export REFRESH_SECRET=your-refresh-secret-key

# Optional: customize server address (default: :8080)
export ADDR=:8080

# Run the server
go run cmd/server/main.go
```

#### Frontend Only (React + TypeScript)

```bash
cd frontend
yarn install
yarn dev
```

The frontend development server typically runs on `http://localhost:5173` (Vite default port). It proxies API requests to `http://localhost:8080`.

## API Documentation

Node Stats provides a comprehensive REST API for accessing all system metrics programmatically. All endpoints return JSON data and support real-time metrics retrieval.

### System Metrics Endpoints

- `GET /api/cpu` - CPU usage metrics including per-core statistics and load averages
- `GET /api/memory` - Memory usage metrics including RAM, swap, and memory statistics
- `GET /api/disk` - Disk usage metrics including I/O statistics and filesystem information
- `GET /api/network` - Network statistics including interface traffic and connection data
- `GET /api/docker` - Docker containers information including resource usage and container status
- `GET /api/system` - System information including hostname, uptime, and platform details
- `GET /api/sensors` - Hardware temperature sensors and thermal monitoring data

### Authentication Endpoints

- `POST /api/auth/register` - User registration
- `POST /api/auth/login` - User authentication
- `POST /api/auth/refresh` - Refresh access token
- `POST /api/auth/logout` - User logout

All API endpoints support CORS and can be integrated with external monitoring tools, automation scripts, or other applications.

### Pulling Pre-built Images

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/lastskywalkerer/node-page:latest

# Run the container (default port 8080)
docker run -p 8080:8080 \
  -e ADDR=:8080 \
  -e JWT_SECRET=your-jwt-secret-key \
  -e REFRESH_SECRET=your-refresh-secret-key \
  -v ./stats.db:/app/stats.db \
  ghcr.io/lastskywalkerer/node-page:latest
```

### Configuration

Node Stats is configured via environment variables:

- `ADDR` - Server address and port (default: `:8080`)
- `GIN_MODE` - Gin framework mode: `release` or `debug` (default: `release`)
- `JWT_SECRET` - Secret key for JWT access tokens (required)
- `REFRESH_SECRET` - Secret key for JWT refresh tokens (required)
- `DB_TYPE` - Database type (default: `sqlite`)
- `DB_DSN` - Database connection string or file path. Local dev: `stats.db`; Docker: `/app/stats.db` (mounted from `./data/docker/stats.db`)
- `DEBUG` - Enable debug mode: `true` or `false` (default: `false`)
- Cluster agent: `MAIN_NODE_URL`, `NODE_ACCESS_TOKEN` — after join, change via Admin → Nodes → **Save connection** or `PUT /api/v1/nodes/agent-cluster-config`.
- **Local metrics host id**: This server always stores its own collected metrics under **`hosts.id = 1`**. Rebuilds update that row (same DB file); use Admin → Nodes to remove stale remote rows if needed.

## Architecture & Technology Stack

Node Stats is built using modern web technologies and follows Clean Architecture principles for maintainability and scalability.

### Technology Stack

- **Frontend**: React 19 + TypeScript 5.9 + Tailwind CSS for a modern, responsive user interface
- **Backend**: Go (Golang) with Gin web framework for high-performance API server
- **Database**: SQLite (default) or PostgreSQL via GORM
- **Monitoring**: Real-time system metrics collection using native Go libraries
- **Authentication**: JWT-based authentication with refresh token support
- **Containerization**: Docker and Docker Compose for easy deployment

### Architecture Principles

- **Clean Architecture**: Separation of concerns with domain, application, infrastructure, and presentation layers
- **RESTful API Design**: Standard REST endpoints for all system metrics
- **Real-time Updates**: Server-Sent Events (SSE) for real-time metric streaming
- **Modular Design**: Feature-based module structure for easy extensibility

## Development

### Project Structure

```
├── cmd/server/          # Application entry point
├── internal/
│   ├── app/            # Application configuration
│   └── modules/        # Feature modules (CPU, Memory, etc.)
├── frontend/           # React frontend
├── .github/workflows/  # CI/CD pipelines
└── Dockerfile         # Container definition
```

## Development

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/node-stats.git
cd node-stats

# One-command build (backend + frontend)
./scripts/build
```

Or build manually: `go build -o bin/server ./cmd/server` and `cd frontend && yarn && yarn build`.

**Helper scripts** (in `scripts/`): `./scripts/dev` — run backend + frontend; `./scripts/build` — build both; `./scripts/clean-db` — remove SQLite DB (prompts for confirmation, reads `DB_DSN` from `.env` or uses `stats.db`).

### Running Tests

```bash
# Backend tests
go test ./...

# Frontend tests
cd frontend
yarn test
```

## Contributing

Contributions are welcome! Whether it's bug fixes, new features, or documentation improvements, your help makes Node Stats better.

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Test thoroughly
5. Commit your changes (`git commit -m 'Add some amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

## Related Projects & Alternatives

Node Stats is inspired by and can be used alongside:
- **Grafana** - Advanced visualization and alerting (Node Stats is lighter)
- **Prometheus** - Time-series database and monitoring (Node Stats uses SQLite)
- **Netdata** - Real-time performance monitoring (Node Stats focuses on simplicity)
- **Portainer** - Docker management UI (Node Stats adds system monitoring)

## Keywords & Tags

**Monitoring Tools**: system monitoring, server monitoring, infrastructure monitoring, docker monitoring, container monitoring, system metrics, server metrics, real-time monitoring, self-hosted monitoring

**Technologies**: Go, Golang, React, TypeScript, Docker, SQLite, REST API, JWT authentication, Gin framework, Tailwind CSS

**Use Cases**: DevOps monitoring, server health monitoring, Docker container stats, system performance tracking, homelab monitoring, development server monitoring, infrastructure health checks

**Features**: CPU monitoring, memory monitoring, disk monitoring, network monitoring, temperature sensors, Docker stats, system information, real-time dashboard, monitoring dashboard

## License

MIT License - see LICENSE file for details
