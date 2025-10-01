/**
 * Main package for the system statistics monitoring application.
 * This is the entry point for the system-stats server that provides
 * real-time monitoring of system performance metrics including CPU,
 * memory, disk, network, and Docker container statistics.
 */
package main

import (
	"system-stats/internal/app/server"
)

/**
 * main is the application entry point.
 * This function initializes and starts the system statistics server,
 * which provides REST API endpoints for metrics collection and retrieval.
 */
func main() {
	server.Run()
}
