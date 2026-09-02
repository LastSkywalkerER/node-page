package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Block is one blocked client IP/CIDR. Replicated by BlockID (cluster-stable),
// rendered by the gateway node as a top-priority ClientIP() deny router, so a
// block takes effect within a second and never puts node-stats on the hot path.
// The table is tiny by design (MaxBlocks) — the traffic stats that lead to a
// block live only in RAM on the gateway node.
type Block struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// BlockID is the cluster-stable identity (short random hex).
	BlockID string `json:"block_id" gorm:"uniqueIndex;size:32;not null"`
	// CIDR is the blocked range in canonical form ("203.0.113.7/32"). The
	// column name is pinned: GORM's naming strategy would otherwise derive
	// "c_id_r" (it splits the "ID" initialism), which broke the ON CONFLICT
	// upsert that references excluded.cidr.
	CIDR string `json:"cidr" gorm:"column:cidr;size:64;not null"`
	// Reason is the operator's note ("scanner", "brute force on /wp-login").
	Reason string `json:"reason,omitempty" gorm:"size:255"`
	// Source: "manual" (admin UI). Reserved for a future "auto" rule engine.
	Source string `json:"source" gorm:"size:16"`
	// CreatedBy is the admin's email (display only).
	CreatedBy string `json:"created_by,omitempty" gorm:"size:255"`
	// ExpiresAt: nil = permanent. Expired rows are swept by the gateway node.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName pins the GORM table name.
func (Block) TableName() string { return "gateway_blocks" }

// MaxBlocks caps the table: every entry becomes part of one Traefik router
// rule, and an unbounded list would balloon the rendered file.
const MaxBlocks = 1024

// BlockSourceManual marks an admin-created block.
const BlockSourceManual = "manual"

// Expired reports whether the block is past its TTL.
func (b Block) Expired(now time.Time) bool {
	return b.ExpiresAt != nil && !b.ExpiresAt.IsZero() && now.After(*b.ExpiresAt)
}

// Contains reports whether ip falls inside the blocked range.
func (b Block) Contains(ip net.IP) bool {
	if ip == nil {
		return false
	}
	_, ipnet, err := net.ParseCIDR(b.CIDR)
	if err != nil {
		return false
	}
	return ipnet.Contains(ip)
}

// NormalizeBlockCIDR validates and canonicalises a user-supplied IP or CIDR.
// Bare IPs become /32 (or /128). Ranges wider than /8 (v4) / /32 (v6) are
// rejected — a fat-finger there would take out half the internet, and the
// admin's own session with it.
func NormalizeBlockCIDR(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("ip or CIDR is required")
	}
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return "", fmt.Errorf("%q is not a valid IP address", s)
		}
		if v4 := ip.To4(); v4 != nil {
			return v4.String() + "/32", nil
		}
		return ip.String() + "/128", nil
	}
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid CIDR", s)
	}
	ones, bits := ipnet.Mask.Size()
	minOnes := 8
	if bits == 128 {
		minOnes = 32
	}
	if ones < minOnes {
		return "", fmt.Errorf("range %q is too wide — /%d or narrower only", s, minOnes)
	}
	_ = ip
	return ipnet.String(), nil
}

// BlockRepository is the persistence contract for gateway blocks. Writes are
// keyed by BlockID so the Raft applier's upsert is idempotent on every replica.
type BlockRepository interface {
	ListBlocks(ctx context.Context) ([]Block, error)
	UpsertBlock(ctx context.Context, b *Block) error
	DeleteBlockByBlockID(ctx context.Context, blockID string) error
}

type gormBlockRepository struct{ db *gorm.DB }

// NewBlockRepository creates the GORM-backed block repository.
func NewBlockRepository(db *gorm.DB) BlockRepository { return &gormBlockRepository{db: db} }

func (r *gormBlockRepository) ListBlocks(ctx context.Context) ([]Block, error) {
	var rows []Block
	err := r.db.WithContext(ctx).Order("created_at DESC, id DESC").Find(&rows).Error
	return rows, err
}

// UpsertBlock inserts or replaces the row identified by BlockID, leaving the
// local autoincrement id alone (ids differ per node; BlockID is the identity).
func (r *gormBlockRepository) UpsertBlock(ctx context.Context, in *Block) error {
	now := time.Now().UTC()
	row := *in
	row.ID = 0
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "block_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"cidr", "reason", "source", "created_by", "expires_at", "updated_at",
		}),
	}).Create(&row).Error
}

func (r *gormBlockRepository) DeleteBlockByBlockID(ctx context.Context, blockID string) error {
	return r.db.WithContext(ctx).Where("block_id = ?", blockID).Delete(&Block{}).Error
}
