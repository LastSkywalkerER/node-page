package config

import "os"

// applyHostProcFromBindMount sets HOST_PROC=/host/proc when a Dokploy-style host
// bind mount exists and HOST_PROC was not explicitly configured. gopsutil then
// reads meminfo, swaps, and other /proc files from the host instead of the container.
func applyHostProcFromBindMount() {
	if os.Getenv("HOST_PROC") != "" {
		return
	}
	if _, err := os.Stat("/host/proc/meminfo"); err != nil {
		return
	}
	_ = os.Setenv("HOST_PROC", "/host/proc")
}
