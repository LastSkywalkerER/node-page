package entities

import (
	"time"

	"gorm.io/gorm"
)

// RefreshToken represents a refresh token for JWT authentication
type RefreshToken struct {
	ID          uint           `gorm:"primaryKey" json:"-"`
	UserID      uint           `gorm:"index;not null" json:"-"`
	JTI         string         `gorm:"uniqueIndex;size:64;not null" json:"-"`
	TokenHash   string         `gorm:"not null" json:"-"`
	ExpiresAt   time.Time      `gorm:"index" json:"-"`
	RevokedAt   *time.Time     `json:"-"`
	CreatedAt   time.Time      `json:"-"`
	User        User           `gorm:"foreignKey:UserID" json:"-"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for the RefreshToken model
func (RefreshToken) TableName() string {
	return "user_refresh_tokens"
}

// IsExpired returns true if the token is expired
func (rt *RefreshToken) IsExpired() bool {
	return time.Now().After(rt.ExpiresAt)
}

// IsRevoked returns true if the token is revoked
func (rt *RefreshToken) IsRevoked() bool {
	return rt.RevokedAt != nil
}

// Revoke marks the token as revoked
func (rt *RefreshToken) Revoke() {
	now := time.Now()
	rt.RevokedAt = &now
}
