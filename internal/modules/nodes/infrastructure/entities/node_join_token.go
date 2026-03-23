package entities

import (
	"time"

	"gorm.io/gorm"
)

// NodeJoinToken represents a one-time token for node registration.
type NodeJoinToken struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Token     string         `gorm:"uniqueIndex;size:64;not null" json:"-"`
	CreatedBy uint           `gorm:"not null" json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	UsedAt    *time.Time     `json:"used_at,omitempty"`
	HostID    *uint          `json:"host_id,omitempty"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for GORM operations.
func (NodeJoinToken) TableName() string {
	return "node_join_tokens"
}
