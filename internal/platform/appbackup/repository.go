package appbackup

import (
	"context"

	"gorm.io/gorm"
)

// Repository stores the local run history. It is deliberately node-local and
// out of Raft: a run is operational trace of one machine's job, not a cluster
// record, and replicating it would put churn into the log for no benefit.
type Repository interface {
	Upsert(ctx context.Context, run *RunEntity) error
	ListByProject(ctx context.Context, hostID uint, project string, limit int) ([]RunEntity, error)
	ListActive(ctx context.Context) ([]RunEntity, error)
	GetByJobID(ctx context.Context, jobID string) (*RunEntity, error)
}

type repository struct{ db *gorm.DB }

// NewRepository wires the run-history store.
func NewRepository(db *gorm.DB) Repository { return &repository{db: db} }

// Upsert writes a run keyed by its job id, so the status poller can update the
// same row as the job progresses without needing to carry the primary key.
func (r *repository) Upsert(ctx context.Context, run *RunEntity) error {
	var existing RunEntity
	err := r.db.WithContext(ctx).Where("job_id = ?", run.JobID).First(&existing).Error
	if err == nil {
		run.ID = existing.ID
		run.CreatedAt = existing.CreatedAt
		return r.db.WithContext(ctx).Save(run).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	return r.db.WithContext(ctx).Create(run).Error
}

func (r *repository) ListByProject(ctx context.Context, hostID uint, project string, limit int) ([]RunEntity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []RunEntity
	q := r.db.WithContext(ctx).Where("project = ?", project)
	if hostID != 0 {
		q = q.Where("host_id = ?", hostID)
	}
	err := q.Order("started_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// ListActive returns runs the controller has not finished, which is what the
// status poller needs to reconcile against the controller's status file.
func (r *repository) ListActive(ctx context.Context) ([]RunEntity, error) {
	var out []RunEntity
	err := r.db.WithContext(ctx).
		Where("phase IN ?", []string{PhaseQueued, PhaseRunning}).
		Order("started_at ASC").Find(&out).Error
	return out, err
}

func (r *repository) GetByJobID(ctx context.Context, jobID string) (*RunEntity, error) {
	var run RunEntity
	err := r.db.WithContext(ctx).Where("job_id = ?", jobID).First(&run).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}
