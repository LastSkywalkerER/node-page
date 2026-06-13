// Package bridge implements the asynchronous cross-cluster replication
// between two independent HashiCorp Raft clusters (typically "local" LAN
// and "public" VPS). Each cluster's leader streams its FSM-applied log
// entries to the peer cluster's leader; the peer applies them with the
// origin cluster id stamped on the command, and dedupes by
// (origin_cluster_id, origin_index) so retries are idempotent.
package bridge

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AppliedRemoteLog records every (origin_cluster_id, origin_index) tuple
// the local cluster has applied from a peer cluster. The receiver consults
// this table before submitting a remote entry to the local Raft log so
// duplicates from sender retries become no-ops.
type AppliedRemoteLog struct {
	OriginClusterID string    `gorm:"primaryKey;size:64"`
	OriginIndex     uint64    `gorm:"primaryKey"`
	OriginNodeID    string    `gorm:"size:64"`
	AppliedAt       time.Time `gorm:"autoCreateTime"`
}

// TableName pins the GORM table name.
func (AppliedRemoteLog) TableName() string { return "applied_remote_log" }

// AutoMigrate creates the dedupe table. Called from the central
// database migrations entry-point.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&AppliedRemoteLog{})
}

// HasApplied returns true if (origin, index) has already been applied locally.
func HasApplied(ctx context.Context, db *gorm.DB, origin string, index uint64) (bool, error) {
	var count int64
	err := db.WithContext(ctx).
		Model(&AppliedRemoteLog{}).
		Where("origin_cluster_id = ? AND origin_index = ?", origin, index).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// AppliedIndexSet returns the subset of indexes (scoped to origin) that have
// already been applied locally, as a set keyed by origin_index. The receiver
// uses this to dedupe a whole batch with ONE query instead of a per-entry
// SELECT count(*) — the dominant DB cost when a busy hub absorbs a metric
// batch per host every few seconds. An empty indexes slice is a no-op.
func AppliedIndexSet(ctx context.Context, db *gorm.DB, origin string, indexes []uint64) (map[uint64]struct{}, error) {
	set := make(map[uint64]struct{}, len(indexes))
	if len(indexes) == 0 {
		return set, nil
	}
	var rows []uint64
	err := db.WithContext(ctx).
		Model(&AppliedRemoteLog{}).
		Where("origin_cluster_id = ? AND origin_index IN ?", origin, indexes).
		Pluck("origin_index", &rows).Error
	if err != nil {
		return nil, err
	}
	for _, idx := range rows {
		set[idx] = struct{}{}
	}
	return set, nil
}

// MarkApplied inserts a dedupe row. Idempotent: a duplicate insert from a
// retried delivery is swallowed silently via INSERT ... ON CONFLICT DO NOTHING
// (works on both SQLite and Postgres), so concurrent redelivery can't race a
// SELECT-then-INSERT into a duplicate-primary-key error.
func MarkApplied(ctx context.Context, db *gorm.DB, origin, originNodeID string, index uint64) error {
	return MarkAppliedBatch(ctx, db, []AppliedRemoteLog{
		{OriginClusterID: origin, OriginIndex: index, OriginNodeID: originNodeID},
	})
}

// MarkAppliedBatch records many dedupe rows in one INSERT ... ON CONFLICT DO
// NOTHING. Idempotent and safe under concurrent redelivery. An empty slice is
// a no-op.
func MarkAppliedBatch(ctx context.Context, db *gorm.DB, rows []AppliedRemoteLog) error {
	if len(rows) == 0 {
		return nil
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&rows).Error
}

// LastAppliedIndex returns the highest origin_index applied from origin.
// Used by the bridge sender to know where to resume from after restart /
// leader change.
func LastAppliedIndex(ctx context.Context, db *gorm.DB, origin string) (uint64, error) {
	var row AppliedRemoteLog
	err := db.WithContext(ctx).
		Where("origin_cluster_id = ?", origin).
		Order("origin_index DESC").
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, nil
		}
		return 0, err
	}
	return row.OriginIndex, nil
}
