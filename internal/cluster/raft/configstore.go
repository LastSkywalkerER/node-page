package raft

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ClusterConfigStore is a key-scoped view of cluster_config: reads hit the
// local DB; writes go through Raft (CmdConfigSet) when enabled so every node
// converges, or straight to the local table when standalone.
type ClusterConfigStore struct {
	db   *gorm.DB
	repl *Replicator
	key  string
}

// NewClusterConfigStore scopes the store to one key.
func NewClusterConfigStore(db *gorm.DB, repl *Replicator, key string) *ClusterConfigStore {
	return &ClusterConfigStore{db: db, repl: repl, key: key}
}

// Get returns the stored value ("" when absent).
func (s *ClusterConfigStore) Get(ctx context.Context) (string, error) {
	return LookupClusterConfig(ctx, s.db, s.key)
}

// Set replicates (or locally writes) the value.
func (s *ClusterConfigStore) Set(ctx context.Context, value string) error {
	if s.repl != nil && s.repl.Enabled() {
		return s.repl.SubmitConfigSet(ctx, s.key, value)
	}
	return upsertClusterConfig(ctx, s.db, s.key, value, time.Now().UTC())
}
