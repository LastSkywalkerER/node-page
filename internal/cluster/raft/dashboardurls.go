package raft

import (
	"context"
	"net"
	"strings"

	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
)

// DashboardURLSource maps a host row to the node-stats dashboard URL of the
// cluster node running on it: the Raft peer whose advertise address carries the
// host's IPv4 → that node's advertised HTTP URL from the peer catalog.
// Implements hosts.DashboardURLSource.
type DashboardURLSource struct {
	db  *gorm.DB
	svc Service
}

// NewDashboardURLSource wires the resolver over the raft service + DB.
func NewDashboardURLSource(db *gorm.DB, svc Service) *DashboardURLSource {
	return &DashboardURLSource{db: db, svc: svc}
}

// DashboardURL returns "" for the local node (the UI uses its own origin),
// for agent-less hosts and whenever Raft is off.
func (s *DashboardURLSource) DashboardURL(ctx context.Context, h hosts.Host) string {
	if s == nil || s.svc == nil || !s.svc.Enabled() || h.ID == hosts.LocalCollectorHostID || h.IPv4 == "" {
		return ""
	}
	st := s.svc.Status()
	nodeID := ""
	for _, p := range st.Peers {
		ip, _, err := net.SplitHostPort(p.Addr)
		if err != nil {
			ip = p.Addr
		}
		if ip == h.IPv4 {
			nodeID = p.ID
			break
		}
	}
	if nodeID == "" || nodeID == st.NodeID {
		return ""
	}
	url, err := LookupPeerURL(ctx, s.db, st.ClusterID, nodeID)
	if err != nil {
		return ""
	}
	return strings.TrimRight(url, "/")
}
