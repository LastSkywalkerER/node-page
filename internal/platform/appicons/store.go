package appicons

// Persistent (DB-backed) layer for the icon byte cache. The in-memory cache in
// service.go is L1 (fast, per-process); this is L2 — the resolved icon BYTES
// survive restarts/redeploys, so a cold process serves icons instantly from the
// database instead of re-resolving every name against the CDNs again. Stored in
// a single dialect-agnostic table (BLOB on SQLite, BYTEA on Postgres) so it
// works the same regardless of the configured database.

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IconCacheEntry is one cached icon (or a cached negative result, OK=false with
// empty Data). Keyed by the normalized lookup key.
type IconCacheEntry struct {
	Key         string    `gorm:"column:key;primaryKey;size:255"`
	Data        []byte    `gorm:"column:data"`
	ContentType string    `gorm:"column:content_type;size:128"`
	OK          bool      `gorm:"column:ok"`
	ExpiresAt   time.Time `gorm:"column:expires_at;index"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

// TableName pins the table name across dialects.
func (IconCacheEntry) TableName() string { return "app_icon_cache" }

// Store persists resolved icons. Implementations must be safe for concurrent use.
type Store interface {
	Load(ctx context.Context, key string) (data []byte, ctype string, ok bool, exp time.Time, found bool)
	Save(ctx context.Context, key string, data []byte, ctype string, ok bool, exp time.Time)
}

type gormStore struct{ db *gorm.DB }

// NewGormStore wires a GORM-backed icon store. db must already be migrated
// (IconCacheEntry is registered in internal/app/database/migrations.go).
func NewGormStore(db *gorm.DB) Store { return &gormStore{db: db} }

func (g *gormStore) Load(ctx context.Context, key string) ([]byte, string, bool, time.Time, bool) {
	var e IconCacheEntry
	if err := g.db.WithContext(ctx).Where("key = ?", key).First(&e).Error; err != nil {
		return nil, "", false, time.Time{}, false
	}
	return e.Data, e.ContentType, e.OK, e.ExpiresAt, true
}

func (g *gormStore) Save(ctx context.Context, key string, data []byte, ctype string, ok bool, exp time.Time) {
	e := IconCacheEntry{
		Key:         key,
		Data:        data,
		ContentType: ctype,
		OK:          ok,
		ExpiresAt:   exp,
		UpdatedAt:   time.Now(),
	}
	// Upsert by key — OnConflict UpdateAll works on both SQLite and Postgres.
	_ = g.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		UpdateAll: true,
	}).Create(&e).Error
}
