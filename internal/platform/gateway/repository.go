package gateway

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the persistence contract for gateway routes. Writes are keyed
// by RouteID so the Raft applier's upsert is idempotent on every replica.
type Repository interface {
	List(ctx context.Context) ([]Route, error)
	GetByRouteID(ctx context.Context, routeID string) (*Route, error)
	Upsert(ctx context.Context, r *Route) error
	DeleteByRouteID(ctx context.Context, routeID string) error
}

type gormRepository struct{ db *gorm.DB }

// NewRepository creates the GORM-backed route repository.
func NewRepository(db *gorm.DB) Repository { return &gormRepository{db: db} }

func (r *gormRepository) List(ctx context.Context) ([]Route, error) {
	var rows []Route
	err := r.db.WithContext(ctx).Order("domain ASC, path_prefix ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *gormRepository) GetByRouteID(ctx context.Context, routeID string) (*Route, error) {
	var row Route
	if err := r.db.WithContext(ctx).Where("route_id = ?", routeID).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Upsert inserts or replaces the row identified by RouteID, leaving the local
// autoincrement id alone (ids differ per node; RouteID is the identity).
func (r *gormRepository) Upsert(ctx context.Context, in *Route) error {
	now := time.Now().UTC()
	row := *in
	row.ID = 0
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "route_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "mode", "domain", "path_prefix",
			"target_scheme", "target_host", "target_port", "target_https_port", "target_host_mac", "target_label",
			"target_insecure_skip_verify", "tls", "basic_auth_users", "ip_allow_list",
			"max_conns_per_ip", "rate_limit_rps", "read_only", "upstream_timeout_seconds", "max_body_bytes",
			"enabled", "updated_at",
		}),
	}).Create(&row).Error
}

func (r *gormRepository) DeleteByRouteID(ctx context.Context, routeID string) error {
	return r.db.WithContext(ctx).Where("route_id = ?", routeID).Delete(&Route{}).Error
}

func itoa(i int) string { return strconv.Itoa(i) }
