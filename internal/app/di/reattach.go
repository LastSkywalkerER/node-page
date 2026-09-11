package di

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"system-stats/internal/app/config"
	raftcluster "system-stats/internal/cluster/raft"
	"system-stats/internal/platform/hostnet"
	setupcfg "system-stats/internal/platform/setup"
)

// ReattachResult reports what an operator-triggered re-attach actually did.
type ReattachResult struct {
	// RaftAddr / HTTPURL are the values this node now advertises.
	RaftAddr string `json:"raft_addr"`
	HTTPURL  string `json:"http_url"`
	// ClusterUpdated is true when a peer applied the new address to the
	// cluster's membership. False means only the local side changed — the
	// node advertises correctly but the others still dial the old address.
	ClusterUpdated bool `json:"cluster_updated"`
	// ClusterError explains a false ClusterUpdated in the operator's terms.
	ClusterError string `json:"cluster_error,omitempty"`
	// PersistError is set when the new settings could not be written to the
	// node's env file: the change is live but would not survive a restart.
	// Not a failure — a deployment may configure everything through the
	// container environment, where there is no file to update.
	PersistError string `json:"persist_error,omitempty"`
}

// SuggestReattachAddr is the address this node would propose for itself: the
// address the machine actually holds, on the Raft port it already listens on.
// Empty when the node has nothing better to offer than what it advertises.
func (c *Container) SuggestReattachAddr() string {
	cfg := c.CurrentRaftConfig()
	ip := hostnet.PrimaryIPv4(context.Background())
	if ip == "" {
		return ""
	}
	port := portOfAddr(cfg.AdvertiseAddr)
	if port == "" {
		port = portOfAddr(cfg.BindAddr)
	}
	if port == "" {
		return ""
	}
	addr := net.JoinHostPort(ip, port)
	if addr == strings.TrimSpace(cfg.AdvertiseAddr) {
		return "" // already advertising it — nothing to propose
	}
	return addr
}

// ReattachNode moves this node to raftAddr: it persists the new advertise
// settings, restarts the Raft layer on them (keeping all data), and asks a
// peer to update the cluster's membership, which only the leader can do and
// this node cannot reach. Empty raftAddr uses SuggestReattachAddr.
//
// Deliberately NOT automatic: an operator confirms (or edits) the address,
// because the machine may hold several and only they know which one the other
// nodes are meant to use.
func (c *Container) ReattachNode(ctx context.Context, raftAddr string) (ReattachResult, error) {
	var res ReattachResult
	cfg := c.CurrentRaftConfig()
	if !c.RaftEnabled() {
		return res, fmt.Errorf("re-attach: the cluster layer is not running on this node")
	}
	addr := strings.TrimSpace(raftAddr)
	if addr == "" {
		addr = c.SuggestReattachAddr()
	}
	if addr == "" {
		return res, fmt.Errorf("re-attach: this node cannot tell which of its addresses the cluster should use — enter one")
	}
	payload := raftcluster.ReattachPayload{
		ClusterID: cfg.ClusterID,
		NodeID:    cfg.NodeID,
		RaftAddr:  addr,
		HTTPURL:   reattachHTTPURL(cfg, addr),
	}
	if err := payload.Validate(); err != nil {
		return res, err
	}
	res.RaftAddr = payload.RaftAddr
	res.HTTPURL = payload.HTTPURL

	// Persist first: whatever happens next, a restart should come back on the
	// corrected address rather than the one nobody can reach. A deployment
	// that has no env file to write (everything set through the container
	// environment) still gets the live change — it is reported, not refused.
	if err := c.persistReattachEnv(payload); err != nil {
		res.PersistError = err.Error()
		if c.logger != nil {
			c.logger.Warn("raft: re-attach could not persist the new address", "error", err)
		}
	}

	// Restart the layer so the transport advertises the new address. Data is
	// kept — this is a re-attach, not a recovery.
	newCfg := cfg
	newCfg.AdvertiseAddr = payload.RaftAddr
	newCfg.AdvertiseURL = payload.HTTPURL
	newCfg.Bootstrap = false // never re-bootstrap: that would fork the cluster
	actCtx, cancel := context.WithTimeout(ctx, reattachActivateTimeout)
	c.activateMu.Lock()
	c.shutdownRaftLocked()
	_, _, err := c.activateLocked(actCtx, newCfg)
	c.activateMu.Unlock()
	cancel()
	if err != nil {
		return res, fmt.Errorf("re-attach: restart the cluster layer on %s: %w", payload.RaftAddr, err)
	}

	// Now tell the cluster where to find us. Best-effort by design: the local
	// side is already correct, and a peer may be busy electing.
	if aerr := raftcluster.AskPeersToReattach(ctx, c.logger, c.db, nil, c.CurrentClusterHMACSecret(), payload); aerr != nil {
		res.ClusterError = aerr.Error()
	} else {
		res.ClusterUpdated = true
	}
	_ = c.AdvertiseSelfNow(ctx)
	return res, nil
}

// persistReattachEnv writes the corrected addresses into the runtime .env so
// they survive a restart. NODE_STATS_IPV4 follows only when the new address is
// a literal IP — it is the machine's advertised address everywhere else in the
// app, and leaving it stale would keep the old value on the machine card.
func (c *Container) persistReattachEnv(p raftcluster.ReattachPayload) error {
	cw := setupcfg.NewConfigWriter()
	cv, err := cw.ReadCurrentConfig()
	if err != nil {
		return err
	}
	if cv == nil {
		return fmt.Errorf("no runtime config file to update")
	}
	cv.RaftAdvertiseAddr = p.RaftAddr
	cv.RaftAdvertisePublicURL = p.HTTPURL
	if host, _, serr := net.SplitHostPort(p.RaftAddr); serr == nil {
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			cv.NodeStatsIPv4 = host
		}
	}
	return cw.WriteConfigFile(cv)
}

// reattachHTTPURL keeps the node's dashboard URL pointing at the same host as
// its Raft address, on the HTTP port it already serves. A URL whose host is
// NOT the old advertised address is left alone: it is a reverse-proxy entry
// that has nothing to do with the address peers cannot dial.
func reattachHTTPURL(cfg config.RaftConfig, newAddr string) string {
	current := strings.TrimRight(strings.TrimSpace(cfg.AdvertiseURL), "/")
	host, _, err := net.SplitHostPort(newAddr)
	if err != nil || host == "" {
		return current
	}
	scheme, port := "http", ""
	if current != "" {
		u, perr := url.Parse(current)
		if perr == nil && u.Host != "" {
			oldHost, _, _ := net.SplitHostPort(strings.TrimSpace(cfg.AdvertiseAddr))
			if u.Hostname() != oldHost {
				return current // a proxy hostname, not the broken address
			}
			if u.Scheme != "" {
				scheme = u.Scheme
			}
			port = u.Port()
		}
	}
	if port == "" {
		if _, p, aerr := net.SplitHostPort(strings.TrimSpace(os.Getenv("ADDR"))); aerr == nil && p != "" {
			port = p
		}
	}
	if port == "" {
		return current
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func portOfAddr(hostport string) string {
	if _, p, err := net.SplitHostPort(strings.TrimSpace(hostport)); err == nil {
		return p
	}
	return ""
}

// reattachActivateTimeout bounds the restart so a stuck transport cannot hold
// the request open.
const reattachActivateTimeout = 20 * time.Second
