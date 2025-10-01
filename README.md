# Node Stats

A comprehensive system monitoring dashboard for Node.js applications with real-time metrics visualization.

## Features

- **Real-time Monitoring**: Live CPU, memory, disk, network, and Docker container metrics
- **Beautiful UI**: Modern dashboard with multiple themes (Neon Terminal, Glass Aurora, Cards Flow, etc.)
- **RESTful API**: Clean API endpoints for all metrics
- **Docker Support**: Monitor Docker containers and system resources
- **Persistent Storage**: SQLite database for historical metrics

## Quick Start

### Using Docker (Recommended)

```bash
# Build and run with Docker Compose
docker-compose up --build

# Or build manually
docker build -t node-stats .
docker run -p 8080:8080 -v $(pwd)/stats.db:/app/stats.db -v /var/run/docker.sock:/var/run/docker.sock:ro node-stats
```

The application will be available at `http://localhost:8080`

### Local Development

#### Backend (Go)
```bash
go mod download
go run cmd/server/main.go
```

#### Frontend (React + TypeScript)
```bash
cd frontend
yarn install
yarn dev
```

## API Endpoints

- `GET /api/cpu` - CPU usage metrics
- `GET /api/memory` - Memory usage metrics
- `GET /api/disk` - Disk usage metrics
- `GET /api/network` - Network statistics
- `GET /api/docker` - Docker containers info
- `GET /api/system` - System information

## GitHub Actions CI/CD

This project includes automated Docker builds via GitHub Actions:

### Setup

1. **Create a GitHub Repository**: Push your code to a GitHub repository

2. **GitHub Token Setup**:
   - GitHub Actions automatically provides `GITHUB_TOKEN` for authentication with GitHub Container Registry
   - No additional secrets are required for this workflow

3. **Push Changes**: The workflow will automatically trigger on push to main branch

### Workflow Features

- **Automated Builds**: Builds Docker image on every push to main
- **Multi-platform**: Supports Linux containers
- **Registry**: Pushes to GitHub Container Registry (ghcr.io)
- **Tagging**: Uses semantic versioning and branch-based tags

### Pulling Pre-built Images

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/YOUR_USERNAME/YOUR_REPO:latest

# Run the container
docker run -p 8080:8080 ghcr.io/YOUR_USERNAME/YOUR_REPO:latest
```

## Architecture

The application follows Clean Architecture principles:

- **Frontend**: React + TypeScript + Tailwind CSS
- **Backend**: Go with Gin framework
- **Database**: SQLite with GORM
- **Monitoring**: Real-time system metrics collection

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

### Adding New Metrics

1. Create new module in `internal/modules/`
2. Implement the standard structure: `application/`, `infrastructure/`, `presentation/`
3. Add API endpoint in the presentation layer
4. Update frontend widgets if needed

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## License

MIT License
