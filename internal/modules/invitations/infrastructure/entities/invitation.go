package entities

import (
	"time"

	"gorm.io/gorm"
)

// UserInvitation represents a one-time invitation link for user registration.
type UserInvitation struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Token     string         `gorm:"uniqueIndex;size:64;not null" json:"-"`
	Email     string         `gorm:"size:255;not null" json:"email"`
	CreatedBy uint           `gorm:"not null" json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UsedAt    *time.Time     `json:"used_at,omitempty"`
	UsedBy    *uint          `json:"used_by,omitempty"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for GORM operations.
func (UserInvitation) TableName() string {
	return "user_invitations"
}
