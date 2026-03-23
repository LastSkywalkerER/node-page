package entities

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
