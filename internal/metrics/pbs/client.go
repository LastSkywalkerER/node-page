// Package pbs implements the Proxmox Backup Server connector: a thin read-only
// API client plus the poller that feeds the host registry / metric pipeline and
// a datastore/backup-health snapshot. PBS exposes a PVE-style REST API on :8007
// whether it runs as a Proxmox guest or on a standalone machine.
package pbs

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one Proxmox Backup Server API endpoint with an API token
// (Authorization: PBSAPIToken=user@realm!tokenid:secret — note the ':' between
// token id and secret, unlike PVE's '='). All calls are read-only; the
// recommended token role is Audit.
type Client struct {
	base    string
	tokenID string
	secret  string
	http    *http.Client
}

// NewClient builds a client for the given endpoint (e.g. https://pbs:8007).
func NewClient(endpoint, tokenID, secret string, skipTLSVerify bool) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("pbs: invalid endpoint %q", endpoint)
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}
	if skipTLSVerify {
		// PBS ships a self-signed certificate by default; the admin opts in.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		base:    u.String(),
		tokenID: tokenID,
		secret:  secret,
		http:    &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}, nil
}

// Host returns the endpoint host (used to qualify the connector fingerprint).
func (c *Client) Host() string {
	if u, err := url.Parse(c.base); err == nil {
		return u.Host
	}
	return c.base
}

// Close releases the idle keep-alive connections held by this client's
// transport (the poller builds a client per cycle and discards it).
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// AuthError marks a 401/403 from the PBS API.
type AuthError struct{ Status string }

func (e *AuthError) Error() string { return "pbs: authentication failed (" + e.Status + ")" }

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api2/json"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PBSAPIToken="+c.tokenID+":"+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("pbs: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &AuthError{Status: resp.Status}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("pbs: GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("pbs: GET %s: decode: %w", path, err)
	}
	return json.Unmarshal(envelope.Data, out)
}

// Version returns the PBS version string (also the cheapest auth check).
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
		Release string `json:"release"`
	}
	if err := c.get(ctx, "/version", &v); err != nil {
		return "", err
	}
	if v.Release != "" {
		return v.Version + "-" + v.Release, nil
	}
	return v.Version, nil
}

// NodeName resolves the API node name (almost always "localhost" on PBS).
func (c *Client) NodeName(ctx context.Context) string {
	var nodes []struct {
		Node string `json:"node"`
	}
	if err := c.get(ctx, "/nodes", &nodes); err == nil {
		for _, n := range nodes {
			if n.Node != "" {
				return n.Node
			}
		}
	}
	return "localhost"
}

// NodeStatus is the host detail from /nodes/{node}/status. PBS uses `root`
// (not PVE's `rootfs`) for the root filesystem and `kversion` for the kernel.
type NodeStatus struct {
	Uptime   int64 `json:"uptime"`
	CPU      float64 `json:"cpu"`
	LoadAvg  []any   `json:"loadavg"`
	KVersion string  `json:"kversion"`
	CPUInfo  struct {
		Model   string `json:"model"`
		CPUs    int    `json:"cpus"`
		Sockets int    `json:"sockets"`
	} `json:"cpuinfo"`
	Memory struct {
		Total uint64 `json:"total"`
		Used  uint64 `json:"used"`
		Free  uint64 `json:"free"`
	} `json:"memory"`
	Root struct {
		Total uint64 `json:"total"`
		Used  uint64 `json:"used"`
		Avail uint64 `json:"avail"`
	} `json:"root"`
}

// NodeStatus fetches host vitals for the PBS node.
func (c *Client) NodeStatus(ctx context.Context, node string) (*NodeStatus, error) {
	var st NodeStatus
	if err := c.get(ctx, "/nodes/"+url.PathEscape(node)+"/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// LoadAvgFloats coerces the version-dependent loadavg representation.
func (st *NodeStatus) LoadAvgFloats() [3]float64 {
	var out [3]float64
	for i := 0; i < len(st.LoadAvg) && i < 3; i++ {
		switch v := st.LoadAvg[i].(type) {
		case float64:
			out[i] = v
		case string:
			fmt.Sscanf(v, "%f", &out[i])
		}
	}
	return out
}

// DatastoreUsage is one row of /status/datastore-usage.
type DatastoreUsage struct {
	Store string `json:"store"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Avail uint64 `json:"avail"`
	Error string `json:"error,omitempty"`
}

// DatastoreUsage lists per-datastore capacity.
func (c *Client) DatastoreUsage(ctx context.Context) ([]DatastoreUsage, error) {
	var items []DatastoreUsage
	err := c.get(ctx, "/status/datastore-usage", &items)
	return items, err
}

// DatastoreGroup is one backup group on a datastore — all snapshots of a single
// backed-up guest/host (a vm/<id>, ct/<id> or host/<name>). From
// /admin/datastore/{store}/groups.
type DatastoreGroup struct {
	BackupType  string `json:"backup-type"` // vm | ct | host
	BackupID    string `json:"backup-id"`
	LastBackup  int64  `json:"last-backup"` // unix seconds of the newest snapshot
	BackupCount int    `json:"backup-count"`
}

// DatastoreGroups lists the backup groups (backed-up machines) on a datastore.
func (c *Client) DatastoreGroups(ctx context.Context, store string) ([]DatastoreGroup, error) {
	var items []DatastoreGroup
	err := c.get(ctx, "/admin/datastore/"+url.PathEscape(store)+"/groups", &items)
	return items, err
}

// Task is one row of /nodes/{node}/tasks. Status is "OK" or an error string for
// finished tasks; empty (with EndTime 0) while still running.
type Task struct {
	UPID      string `json:"upid"`
	Type      string `json:"type"`     // worker type: backup | verify | garbage_collection | prune | sync | …
	ID        string `json:"id"`       // worker id, e.g. "datastore:host/name"
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
	Status    string `json:"status"`
}

// Tasks returns the most recent tasks (newest first) for backup-health.
func (c *Client) Tasks(ctx context.Context, node string, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 100
	}
	var items []Task
	path := fmt.Sprintf("/nodes/%s/tasks?start=0&limit=%d", url.PathEscape(node), limit)
	err := c.get(ctx, path, &items)
	return items, err
}
