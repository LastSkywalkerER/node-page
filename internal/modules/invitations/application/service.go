package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/invitations/infrastructure/entities"
	invrepos "system-stats/internal/modules/invitations/infrastructure/repositories"
)

// Service defines the invitation service interface.
type Service interface {
	CreateInvitation(ctx context.Context, adminUserID uint, baseURL string, email string) (token, link string, err error)
	ValidateToken(ctx context.Context, token string) (*entities.UserInvitation, error)
	Consume(ctx context.Context, invID uint, usedBy uint) error
}

type service struct {
	logger   *log.Logger
	invRepo  invrepos.InvitationRepository
}

// NewService creates a new invitation service.
func NewService(logger *log.Logger, invRepo invrepos.InvitationRepository) Service {
	return &service{
		logger:  logger,
		invRepo: invRepo,
	}
}

// CreateInvitation creates a new invitation and returns the token and full link.
func (s *service) CreateInvitation(ctx context.Context, adminUserID uint, baseURL string, email string) (token, link string, err error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return "", "", errors.New("email is required")
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("failed to generate token: %w", err)
	}
	token = hex.EncodeToString(b)

	inv := &entities.UserInvitation{
		Token:     token,
		Email:     email,
		CreatedBy: adminUserID,
	}
	if err := s.invRepo.Create(ctx, inv); err != nil {
		return "", "", fmt.Errorf("failed to create invitation: %w", err)
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		u = &url.URL{Path: "/auth"}
	}
	q := u.Query()
	q.Set("invite", token)
	u.RawQuery = q.Encode()
	if u.Path == "" || u.Path == "/" {
		u.Path = "/auth"
	}
	link = u.String()

	s.logger.Info("Invitation created", "created_by", adminUserID, "token_prefix", token[:8]+"...")
	return token, link, nil
}

// ValidateToken validates the invitation token and returns the invitation if valid.
// Does not consume the token; caller must call Consume after user creation.
func (s *service) ValidateToken(ctx context.Context, token string) (*entities.UserInvitation, error) {
	if token == "" {
		return nil, errors.New("invitation token is required")
	}
	inv, err := s.invRepo.FindByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, errors.New("invalid or already used invitation token")
	}
	if strings.TrimSpace(inv.Email) == "" {
		return nil, errors.New("invalid invitation: email not set (legacy invitation)")
	}
	return inv, nil
}

// Consume marks the invitation as used.
func (s *service) Consume(ctx context.Context, invID uint, usedBy uint) error {
	return s.invRepo.MarkUsed(ctx, invID, usedBy)
}
