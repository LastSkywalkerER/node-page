package raft

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// fsmApplyState records how far this node's DURABLE FSM state (the SQLite
// tables the appliers write) has been advanced.
//
// hashicorp/raft replays every log entry after the last SNAPSHOT on each
// restart, because a classic FSM lives in memory and has to be rebuilt. Ours
// does not: it lives in SQLite and survives the process. Without a watermark
// the replay re-runs days of history against state that already contains its
// effects — harmless for a pure upsert, but NOT for the appliers that branch on
// current state. A host registration is the sharp edge: replaying the
// pre-move registration of a machine whose stable identity has since changed
// (bare metal → VM) no longer matches the row it originally wrote, so the
// identity guard correctly reads it as "a different machine" and CREATES a
// duplicate — a ghost card for a machine that moved days ago.
//
// The watermark is node-LOCAL bookkeeping (like applied_remote_log), never
// replicated and deliberately absent from managedTables: it describes this
// node's own SQLite, not cluster state.
type fsmApplyState struct {
	ID           uint `gorm:"primaryKey"`
	AppliedIndex uint64
	UpdatedAt    time.Time
}

func (fsmApplyState) TableName() string { return "raft_apply_state" }

// fsmApplyStateRowID is the single row's fixed primary key — the table holds
// exactly one watermark per node.
const fsmApplyStateRowID uint = 1

// AppliedIndexStore persists the FSM apply watermark across restarts.
type AppliedIndexStore interface {
	// Load returns the last persisted index, or 0 when none was ever written.
	Load(ctx context.Context) (uint64, error)
	// Store records index as the new watermark.
	Store(ctx context.Context, index uint64) error
}

type sqlAppliedIndexStore struct{ db *gorm.DB }

// NewAppliedIndexStore wires the SQLite/Postgres-backed watermark store.
// Returns nil for a nil DB so callers can wire it unconditionally.
func NewAppliedIndexStore(db *gorm.DB) AppliedIndexStore {
	if db == nil {
		return nil
	}
	return &sqlAppliedIndexStore{db: db}
}

func (s *sqlAppliedIndexStore) Load(ctx context.Context) (uint64, error) {
	var row fsmApplyState
	err := s.db.WithContext(ctx).Where("id = ?", fsmApplyStateRowID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return row.AppliedIndex, nil
}

func (s *sqlAppliedIndexStore) Store(ctx context.Context, index uint64) error {
	row := fsmApplyState{ID: fsmApplyStateRowID, AppliedIndex: index, UpdatedAt: time.Now()}
	return s.db.WithContext(ctx).
		Where("id = ?", fsmApplyStateRowID).
		Assign(map[string]any{"applied_index": index, "updated_at": row.UpdatedAt}).
		FirstOrCreate(&row).Error
}
