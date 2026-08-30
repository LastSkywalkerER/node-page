package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/platform/gateway"
)

// BlockRequest is the create-block body. AdminIP / AdminEmail are filled by
// the handler from the authenticated request, never from the client body.
type BlockRequest struct {
	CIDR   string `json:"cidr"`
	Reason string `json:"reason"`
	// TTLHours: 0 = permanent.
	TTLHours float64 `json:"ttl_hours"`
	// Force skips the "this would block your own IP" guard.
	Force bool `json:"force"`

	AdminIP    string `json:"-"`
	AdminEmail string `json:"-"`
}

// ListBlocks returns every replicated client block (expired ones included —
// the sweeper removes them; the UI greys them out meanwhile).
func (s *service) ListBlocks(ctx context.Context) ([]gateway.Block, error) {
	if s.blocks == nil {
		return []gateway.Block{}, nil
	}
	rows, err := s.blocks.ListBlocks(ctx)
	if rows == nil {
		rows = []gateway.Block{}
	}
	return rows, err
}

// CreateBlock validates and persists a client block (idempotent per CIDR: a
// duplicate updates the existing entry's reason/TTL instead of adding a row).
func (s *service) CreateBlock(ctx context.Context, req BlockRequest) (*gateway.Block, error) {
	if s.blocks == nil {
		return nil, fmt.Errorf("%w: block storage unavailable", ErrValidation)
	}
	cidr, err := gateway.NormalizeBlockCIDR(req.CIDR)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrValidation, err.Error())
	}
	b := gateway.Block{
		BlockID:   newRouteID(),
		CIDR:      cidr,
		Reason:    strings.TrimSpace(req.Reason),
		Source:    gateway.BlockSourceManual,
		CreatedBy: strings.TrimSpace(req.AdminEmail),
		CreatedAt: time.Now().UTC(),
	}
	if req.TTLHours > 0 {
		exp := time.Now().UTC().Add(time.Duration(req.TTLHours * float64(time.Hour)))
		b.ExpiresAt = &exp
	}
	// Self-lockout guard: refuse to block the range the admin is calling from
	// (they'd cut their own access to every routed service) unless forced.
	if !req.Force {
		if ip := net.ParseIP(strings.TrimSpace(req.AdminIP)); ip != nil && b.Contains(ip) {
			return nil, fmt.Errorf("%w: %s covers your own IP (%s) — you would lose access to every routed service; set force to do it anyway", ErrValidation, cidr, req.AdminIP)
		}
	}
	existing, err := s.blocks.ListBlocks(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range existing {
		if e.CIDR == cidr {
			b.BlockID = e.BlockID // update in place
			b.CreatedAt = e.CreatedAt
		}
	}
	if b.BlockID == "" {
		return nil, fmt.Errorf("%w: bad block id", ErrValidation)
	}
	if len(existing) >= gateway.MaxBlocks {
		return nil, fmt.Errorf("%w: block list is full (%d entries) — remove stale entries first", ErrValidation, gateway.MaxBlocks)
	}
	if err := s.persistBlock(ctx, b); err != nil {
		return nil, err
	}
	return &b, nil
}

// DeleteBlock removes a block everywhere.
func (s *service) DeleteBlock(ctx context.Context, blockID string) error {
	if s.blocks == nil {
		return nil
	}
	var err error
	if s.raft != nil && s.raft.Enabled() {
		err = s.raft.SubmitGatewayBlockDelete(ctx, blockID)
	} else {
		err = s.blocks.DeleteBlockByBlockID(ctx, blockID)
	}
	if err == nil {
		s.poke()
	}
	return err
}

func (s *service) persistBlock(ctx context.Context, b gateway.Block) error {
	var err error
	if s.raft != nil && s.raft.Enabled() {
		err = s.raft.SubmitGatewayBlockUpsert(ctx, b)
	} else {
		err = s.blocks.UpsertBlock(ctx, &b)
	}
	if err == nil {
		s.poke()
	}
	return err
}

// RunBlockExpiry sweeps expired blocks once a minute — on the gateway node
// only, so exactly one node submits the (replicated) deletes. Expired blocks
// stop matching at render time immediately; this just tidies the table.
func (s *service) RunBlockExpiry(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.blocks == nil || s.mat == nil || !s.mat.Status().IsGatewayNode {
			continue
		}
		rows, err := s.blocks.ListBlocks(ctx)
		if err != nil {
			continue
		}
		now := time.Now().UTC()
		for _, b := range rows {
			if b.Expired(now) {
				if err := s.DeleteBlock(ctx, b.BlockID); err != nil && s.logger != nil {
					s.logger.Warn("gateway: expired block sweep", "block_id", b.BlockID, "error", err)
				}
			}
		}
	}
}

// Connections returns the gateway node's live connection stats. On any other
// node it reports unavailable with reason "not_gateway" — the handler then
// proxies to the gateway node's dashboard URL.
func (s *service) Connections(ctx context.Context, topN, recentN int) (*ConnectionsView, error) {
	if s.mat == nil {
		return &ConnectionsView{Available: false, Reason: "connection tracking unavailable on this node"}, nil
	}
	st := s.mat.Status()
	if !st.IsGatewayNode {
		return &ConnectionsView{Available: false, Reason: "not_gateway"}, nil
	}
	v := s.mat.Tracker().Snapshot(topN, recentN)
	if !v.Available && st.Mode == gateway.ModeExternal {
		v.Reason = "external mode: set NODE_STATS_GATEWAY_ACCESSLOG on this node to your Traefik's JSON access log to enable connection stats"
	}
	// Flag clients already covered by an active block.
	if v.Available && s.blocks != nil && len(v.Top) > 0 {
		if blocks, err := s.blocks.ListBlocks(ctx); err == nil {
			now := time.Now().UTC()
			for i := range v.Top {
				ip := net.ParseIP(v.Top[i].IP)
				for _, b := range blocks {
					if !b.Expired(now) && b.Contains(ip) {
						v.Top[i].IsBlocked = true
						break
					}
				}
			}
		}
	}
	return v, nil
}

// GatewayNodeURL resolves the gateway node's node-stats dashboard URL (for
// proxying /gateway/connections from other nodes). Empty when unknown or when
// this node IS the gateway.
func (s *service) GatewayNodeURL(ctx context.Context) string {
	if s.mat != nil && s.mat.Status().IsGatewayNode {
		return ""
	}
	cfg, err := LoadConfig(ctx, s.cfg)
	if err != nil || !cfg.Enabled || cfg.NodeMAC == "" || s.hosts == nil {
		return ""
	}
	h, err := s.hosts.GetHostByMacAddress(ctx, cfg.NodeMAC)
	if err != nil || h == nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return ""
		}
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(h.DashboardURL), "/")
}
