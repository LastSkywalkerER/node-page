package raft

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

// clusterConfig is the replicated key/value store backing cluster-wide
// settings: JWT/refresh secrets, bridge ack cursors and arbitrary config
// values pushed via CmdConfigSet.
type clusterConfig struct {
	Key       string    `gorm:"primaryKey;size:128"`
	Value     string    `gorm:"type:text"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// TableName pins the GORM table name.
func (clusterConfig) TableName() string { return "cluster_config" }

// peerNodeAdvertise is the URL catalog row published by CmdPeerNodeAdvertise.
// One row per (cluster_id, node_id); URL is what the cross-cluster bridge
// and admin UI use.
type peerNodeAdvertise struct {
	ClusterID    string    `gorm:"primaryKey;size:64"`
	NodeID       string    `gorm:"primaryKey;size:64"`
	URL          string    `gorm:"type:text"`
	Capabilities string    `gorm:"type:text"` // comma-separated
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

func (peerNodeAdvertise) TableName() string { return "peer_node_advertise" }

// clusterJoinToken is the one-shot bootstrap token an admin generates so a
// fresh node can pull a snapshot. Stored hashed; the plaintext is returned
// once when CmdJoinTokenIssue is submitted and never persisted.
type clusterJoinToken struct {
	TokenHash  string     `gorm:"primaryKey;size:64"`
	ExpiresAt  time.Time  `gorm:"index"`
	CreatedBy  uint       `gorm:"index"`
	CreatedAt  time.Time  `gorm:"autoCreateTime"`
	ConsumedAt *time.Time `gorm:"index"`
	ByNodeID   string     `gorm:"size:64"`
	ByNodeAddr string     `gorm:"size:128"`
}

func (clusterJoinToken) TableName() string { return "cluster_join_tokens" }

// AutoMigrate creates the Raft-managed tables. Called from the central
// database migrations entry-point.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&clusterConfig{}, &peerNodeAdvertise{}, &clusterJoinToken{})
}

// upsertClusterConfig writes a single key/value pair to cluster_config.
// Exported through internal helpers used by appliers and the lookup API.
func upsertClusterConfig(ctx context.Context, db *gorm.DB, key, value string, ts time.Time) error {
	row := clusterConfig{Key: key, Value: value, UpdatedAt: ts}
	return db.WithContext(ctx).
		Where("key = ?", key).
		Assign(map[string]any{"value": value, "updated_at": ts}).
		FirstOrCreate(&row).Error
}

// LookupClusterConfig returns the value for key, or "" if absent.
// Safe to call before Raft is up — it just hits SQLite.
func LookupClusterConfig(ctx context.Context, db *gorm.DB, key string) (string, error) {
	var row clusterConfig
	err := db.WithContext(ctx).Where("key = ?", key).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return row.Value, nil
}

// PeerAdvertise is the projection returned by ListPeerAdvertises.
type PeerAdvertise struct {
	ClusterID    string
	NodeID       string
	URL          string
	Capabilities []string
	UpdatedAt    time.Time
}

// ListPeerAdvertises returns every URL advertised through CmdPeerNodeAdvertise.
// The bridge URL-picker filters this list by cluster on its own.
func ListPeerAdvertises(ctx context.Context, db *gorm.DB) ([]PeerAdvertise, error) {
	var rows []peerNodeAdvertise
	if err := db.WithContext(ctx).Order("cluster_id ASC, node_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]PeerAdvertise, 0, len(rows))
	for _, r := range rows {
		out = append(out, PeerAdvertise{
			ClusterID:    r.ClusterID,
			NodeID:       r.NodeID,
			URL:          r.URL,
			Capabilities: splitCSV(r.Capabilities),
			UpdatedAt:    r.UpdatedAt,
		})
	}
	return out, nil
}

// LookupJoinToken returns the join token row for tokenHash, or nil if missing.
func LookupJoinToken(ctx context.Context, db *gorm.DB, tokenHash string) (*ClusterJoinTokenView, error) {
	var row clusterJoinToken
	err := db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &ClusterJoinTokenView{
		ExpiresAt:  row.ExpiresAt,
		ConsumedAt: row.ConsumedAt,
		ByNodeID:   row.ByNodeID,
	}, nil
}

// ClusterJoinTokenView is the public, read-only projection of a join token.
type ClusterJoinTokenView struct {
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	ByNodeID   string
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func joinCSV(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ",")
}
