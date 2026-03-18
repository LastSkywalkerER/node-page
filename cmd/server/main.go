// System Stats API Server — self-hosted system monitoring dashboard.
//
// Real-time monitoring of CPU, memory, disk, network, Docker containers, and sensors.
// Metrics are collected every 5 seconds and streamed via SSE.
//
// @title           System Stats API
// @version         1.0
// @description     Self-hosted system monitoring dashboard API. Provides CPU, memory, disk, network, Docker, and sensor metrics with SSE streaming.
// @host            localhost:8080
// @BasePath        /api/v1
//
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
package main

import (
	"system-stats/internal/app/server"
)

func main() {
	server.Run()
}
