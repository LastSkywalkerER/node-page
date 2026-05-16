package nodes

import (
	"time"

	"gorm.io/gorm"
)

// NodeCredential stores the hashed node access token for push authentication.
type NodeCredential struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	HostID    uint           `gorm:"uniqueIndex;not null" json:"host_id"`
	TokenHash string         `gorm:"size:64;not null" json:"-"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for GORM operations.
func (NodeCredential) TableName() string {
	return "node_credentials"
}

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
