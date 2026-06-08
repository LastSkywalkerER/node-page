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
	"fmt"
	"os"

	"system-stats/internal/app/server"
	"system-stats/internal/platform/controller"
	"system-stats/internal/platform/setup"
	"system-stats/internal/platform/update"
)

func main() {
	// Subcommands let the single binary play multiple roles (same image, different
	// entrypoint). No args = the HTTP server (unchanged default).
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "controller":
			// Privileged compose-applying sidecar (docker deployments only).
			controller.Run()
			return
		case "gen-compose":
			// Emit the canonical base docker-compose.yml (sqlite default) to
			// stdout so the installer bootstraps an identical stack to what the
			// controller later regenerates. Image overridable via NODE_STATS_IMAGE.
			fmt.Print(setup.BuildComposeContent(setup.DesiredState{
				DBMode: setup.DBModeSQLite,
				Image:  os.Getenv("NODE_STATS_IMAGE"),
			}))
			return
		case "update":
			// Native self-update from the latest GitHub release. `--check` only
			// reports availability (exit 10 when an update exists).
			checkOnly := len(os.Args) > 2 && (os.Args[2] == "--check" || os.Args[2] == "-check")
			update.RunCLI(checkOnly)
			return
		}
	}
	server.Run()
}
