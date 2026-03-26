package pusher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

var loggedPushDisabled sync.Once

// PushPayload is the minimal payload sent to main node.
type PushPayload struct {
	Status             string  `json:"status"`
	UptimeSeconds      int64   `json:"uptime_seconds"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`
	MemoryUsagePercent float64 `json:"memory_usage_percent"`
	HostName           string  `json:"host_name,omitempty"`
	HostIPv4           string  `json:"host_ipv4,omitempty"`
}

// Push sends metrics to the main node. Non-blocking; runs in goroutine.
// hostName and hostIPv4 should be the agent's effective CollectHostInfo values (wizard NODE_STATS_* included).
func Push(ctx context.Context, logger *log.Logger, mainURL, token string, metrics map[string]interface{}, hostName, hostIPv4 string) {
	if mainURL == "" || token == "" {
		loggedPushDisabled.Do(func() {
			logger.Warn("Cluster push is disabled — set MAIN_NODE_URL and NODE_ACCESS_TOKEN so the main node receives heartbeats (last_seen). Connect from the agent UI or add these to .env / Docker env.")
		})
		return
	}

	payload := buildPayload(metrics, hostName, hostIPv4)
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to marshal push payload", "error", err)
		return
	}

	url := mainURL + "/api/v1/nodes/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logger.Error("Failed to create push request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("Push to main node failed", "error", err, "url", mainURL)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		logger.Warn("Push to main node returned non-success", "status", resp.StatusCode, "url", mainURL)
	}
}

func buildPayload(metrics map[string]interface{}, hostName, hostIPv4 string) PushPayload {
	payload := PushPayload{
		Status:        "ok",
		UptimeSeconds: 0,
		HostName:      hostName,
		HostIPv4:      hostIPv4,
	}

	if cpu, ok := metrics["cpu"].(map[string]interface{}); ok {
		if v, ok := cpu["usage_percent"].(float64); ok {
			payload.CPUUsagePercent = v
		}
	}
	if mem, ok := metrics["memory"].(map[string]interface{}); ok {
		if v, ok := mem["usage_percent"].(float64); ok {
			payload.MemoryUsagePercent = v
		}
	}
	// Uptime from host - we don't have it in metrics directly; use 0 for now
	// Could add from host.Info() if needed
	_ = fmt.Sprint(payload.UptimeSeconds) // avoid unused

	return payload
}
