package repositories

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/modules/invitations/infrastructure/entities"
)

// InvitationRepository defines the interface for invitation data operations.
type InvitationRepository interface {
	Create(ctx context.Context, inv *entities.UserInvitation) error
	FindByToken(ctx context.Context, token string) (*entities.UserInvitation, error)
	MarkUsed(ctx context.Context, id uint, usedBy uint) error
}

type invitationRepository struct {
	db *gorm.DB
}

// NewInvitationRepository creates a new invitation repository.
func NewInvitationRepository(db *gorm.DB) InvitationRepository {
	return &invitationRepository{db: db}
}

// Create creates a new invitation in the database.
func (r *invitationRepository) Create(ctx context.Context, inv *entities.UserInvitation) error {
	return r.db.WithContext(ctx).Create(inv).Error
}

// FindByToken finds an invitation by token.
func (r *invitationRepository) FindByToken(ctx context.Context, token string) (*entities.UserInvitation, error) {
	var inv entities.UserInvitation
	err := r.db.WithContext(ctx).Where("token = ? AND used_at IS NULL", token).First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// MarkUsed marks an invitation as used.
func (r *invitationRepository) MarkUsed(ctx context.Context, id uint, usedBy uint) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&entities.UserInvitation{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"used_at": now,
			"used_by": usedBy,
		}).Error
}
