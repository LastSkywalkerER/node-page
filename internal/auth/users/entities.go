package users

import (
	"time"

	"gorm.io/gorm"
)

// User represents a user account in the system
type User struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	Email        string         `gorm:"uniqueIndex;size:320;not null" json:"email" validate:"required,email"`
	PasswordHash string         `gorm:"not null" json:"-"`
	Role         string         `gorm:"not null;default:USER" json:"role"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for the User model
func (User) TableName() string {
	return "user_accounts"
}

// BeforeCreate hook to set default role if not provided
func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.Role == "" {
		u.Role = "USER"
	}
	return nil
}

// IsAdmin returns true if the user has admin role
func (u *User) IsAdmin() bool {
	return u.Role == "ADMIN"
}

// RefreshToken represents a refresh token for JWT authentication
type RefreshToken struct {
	ID        uint           `gorm:"primaryKey" json:"-"`
	UserID    uint           `gorm:"index;not null" json:"-"`
	JTI       string         `gorm:"uniqueIndex;size:64;not null" json:"-"`
	TokenHash string         `gorm:"not null" json:"-"`
	ExpiresAt time.Time      `gorm:"index" json:"-"`
	RevokedAt *time.Time     `json:"-"`
	CreatedAt time.Time      `json:"-"`
	User      User           `gorm:"foreignKey:UserID" json:"-"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
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
