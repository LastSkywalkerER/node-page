package connectors

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Repository defines connector persistence. Replicated mutations (Upsert /
// SetEnabled / DeleteByFingerprint) are keyed by fingerprint — the
// cluster-stable identity — because local autoincrement ids differ per node.
type Repository interface {
	List(ctx context.Context) ([]Connector, error)
	GetByID(ctx context.Context, id uint) (*Connector, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*Connector, error)
	// Upsert creates or updates the row with the given fingerprint. The local
	// runtime status columns (status/last_sync_at/last_error) are preserved.
	Upsert(ctx context.Context, c *Connector) error
	DeleteByFingerprint(ctx context.Context, fingerprint string) error
	// UpdateLocalStatus records poller state. Local-only — never replicated.
	UpdateLocalStatus(ctx context.Context, id uint, status, lastError string, lastSync time.Time) error
}

type repository struct {
	db *gorm.DB
}

// NewRepository creates a GORM-backed connector repository.
func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) List(ctx context.Context) ([]Connector, error) {
	var rows []Connector
	err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *repository) GetByID(ctx context.Context, id uint) (*Connector, error) {
	var row Connector
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *repository) GetByFingerprint(ctx context.Context, fingerprint string) (*Connector, error) {
	var row Connector
	err := r.db.WithContext(ctx).Where("fingerprint = ?", fingerprint).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *repository) Upsert(ctx context.Context, c *Connector) error {
	var existing Connector
	err := r.db.WithContext(ctx).Where("fingerprint = ?", c.Fingerprint).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if c.Status == "" {
			c.Status = StatusPending
		}
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&Connector{}).
		Where("id = ?", existing.ID).
		Updates(map[string]interface{}{
			"type":            c.Type,
			"endpoint":        c.Endpoint,
			"token_id":        c.TokenID,
			"secret_enc":      c.SecretEnc,
			"skip_tls_verify": c.SkipTLSVerify,
			"config":          c.Config,
			"enabled":         c.Enabled,
			"updated_at":      time.Now(),
		}).Error
}

func (r *repository) DeleteByFingerprint(ctx context.Context, fingerprint string) error {
	return r.db.WithContext(ctx).Where("fingerprint = ?", fingerprint).Delete(&Connector{}).Error
}

func (r *repository) UpdateLocalStatus(ctx context.Context, id uint, status, lastError string, lastSync time.Time) error {
	updates := map[string]interface{}{
		"status":     status,
		"last_error": lastError,
	}
	if !lastSync.IsZero() {
		updates["last_sync_at"] = lastSync
	}
	return r.db.WithContext(ctx).Model(&Connector{}).Where("id = ?", id).Updates(updates).Error
}
