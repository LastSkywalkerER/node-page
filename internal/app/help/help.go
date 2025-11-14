/**
 * Package help provides help message display functionality.
 * This package handles displaying application documentation, environment variables
 * description, and usage examples.
 */
package help

import (
	"fmt"
	"os"
)

/**
 * Show displays the help message with application description, environment variables
 * documentation, and example .env configuration file.
 * This function outputs comprehensive documentation to stdout and exits.
 */
func Show() {
	fmt.Println(`System Stats API Server

A comprehensive system monitoring dashboard with real-time metrics visualization.
The application monitors CPU, memory, disk, network, Docker containers, and system sensors.

Configuration:
  All configuration is done through environment variables or a .env file.
  Create a .env file in the application directory or set environment variables.

Usage:
  system-stats              # Start server with default configuration
  system-stats -help        # Show this help message

Environment Variables:

  Server Configuration:
    ADDR                    HTTP server listening address (default: ":8080")
                            Example: ADDR=:8080 or ADDR=0.0.0.0:3000

    GIN_MODE                Gin framework mode: "debug" or "release" (default: "release")
                            Example: GIN_MODE=debug

    DEBUG                   Enable debug logging: "true", "1", "false", or "0" (default: "false")
                            Example: DEBUG=true

  Database Configuration:
    DB_TYPE                 Database type: "sqlite" (default: "sqlite")
                            Example: DB_TYPE=sqlite

    DB_DSN                  SQLite database file path (default: "stats.db")
                            Example: DB_DSN=./data/stats.db

  Authentication Configuration:
    JWT_SECRET              Secret key for JWT access tokens (required)
                            Example: JWT_SECRET=your-jwt-secret-key-change-in-production

    REFRESH_SECRET          Secret key for JWT refresh tokens (required)
                            Example: REFRESH_SECRET=your-refresh-secret-key-change-in-production

Example .env file:

  # Server Configuration
  ADDR=:8080
  GIN_MODE=release
  DEBUG=false

  # Database Configuration
  DB_TYPE=sqlite
  DB_DSN=stats.db

  # Authentication Configuration
  JWT_SECRET=your-jwt-secret-key-change-in-production
  REFRESH_SECRET=your-refresh-secret-key-change-in-production

API Endpoints:

  Authentication (Public):
    POST /api/v1/auth/register    - Register a new user
    POST /api/v1/auth/login       - Login and get access token
    POST /api/v1/auth/refresh     - Refresh access token
    POST /api/v1/auth/logout      - Logout (requires authentication)

  User Management (Protected):
    GET    /api/v1/users/me        - Get current user information
    GET    /api/v1/users           - List all users (admin only)
    PATCH  /api/v1/users/:id       - Update user role (admin only)
    DELETE /api/v1/users/:id       - Delete user (admin only)

  Metrics (Protected):
    GET /api/v1/metrics/current    - Current system metrics for dashboard
    GET /api/v1/cpu                - CPU statistics (JSON)
    GET /api/v1/memory             - Memory statistics (JSON)
    GET /api/v1/disk               - Disk statistics (JSON)
    GET /api/v1/network            - Network statistics (JSON)
    GET /api/v1/docker             - Docker containers statistics (JSON)
    GET /api/v1/sensors            - Temperature sensors readings (JSON)
    GET /api/v1/hosts              - All registered hosts (JSON)
    GET /api/v1/hosts/current      - Current host information (JSON)
    POST /api/v1/hosts/register    - Register/update current host
    GET /api/v1/health             - Health check (JSON)

API Usage Examples:

  # Register a new user
  curl -X POST http://localhost:8080/api/v1/auth/register \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","email":"admin@example.com","password":"secure-password"}'

  # Login
  curl -X POST http://localhost:8080/api/v1/auth/login \
    -H "Content-Type: application/json" \
    -d '{"email":"admin@example.com","password":"secure-password"}'

  # Get CPU metrics (requires authentication)
  curl http://localhost:8080/api/v1/cpu \
    -H "Authorization: Bearer YOUR_ACCESS_TOKEN"

  # Get memory metrics
  curl http://localhost:8080/api/v1/memory \
    -H "Authorization: Bearer YOUR_ACCESS_TOKEN"

  # Get health check
  curl http://localhost:8080/api/v1/health \
    -H "Authorization: Bearer YOUR_ACCESS_TOKEN"`)
}

/**
 * ShowAndExit displays the help message and exits the application with code 0.
 * This is a convenience function for handling the -help flag.
 */
func ShowAndExit() {
	Show()
	os.Exit(0)
}
