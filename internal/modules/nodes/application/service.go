package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	healthapp "system-stats/internal/modules/health/application"
	hostentities "system-stats/internal/modules/hosts/infrastructure/entities"
	hostrepos "system-stats/internal/modules/hosts/infrastructure/repositories"
	clusterconfig "system-stats/internal/modules/nodes/infrastructure/cluster_config"
	nodeentities "system-stats/internal/modules/nodes/infrastructure/entities"
	noderepos "system-stats/internal/modules/nodes/infrastructure/repositories"
)

// Service defines the nodes service interface.
type Service interface {
	CreateNodeInvite(ctx context.Context, adminUserID uint, baseURL string) (link string, err error)
	Join(ctx context.Context, token string, hostInfo hostentities.HostInfo) (hostID uint, nodeAccessToken string, err error)
	ValidateNodeToken(ctx context.Context, token string) (hostID uint, err error)
	HandlePush(ctx context.Context, hostID uint) error
	// RegenerateNodeAccessToken replaces the push token; returns plaintext once (old token stops working immediately).
	RegenerateNodeAccessToken(ctx context.Context, hostID uint) (nodeAccessToken string, err error)
	GetClusterUIStatus(ctx context.Context, currentHostID uint, publicBaseURL string) (ClusterUIStatus, error)
	DeleteRemoteHost(ctx context.Context, hostID, currentHostID uint) error
	UpdateAgentClusterConfig(mainNodeURL, nodeAccessToken string) error
}

type service struct {
	logger        *log.Logger
	joinTokenRepo noderepos.NodeJoinTokenRepository
	credRepo      noderepos.NodeCredentialRepository
	hostRepo      hostrepos.HostRepository
}

// NewService creates a new nodes service.
func NewService(
	logger *log.Logger,
	joinTokenRepo noderepos.NodeJoinTokenRepository,
	credRepo noderepos.NodeCredentialRepository,
	hostRepo hostrepos.HostRepository,
) Service {
	return &service{
		logger:        logger,
		joinTokenRepo: joinTokenRepo,
		credRepo:      credRepo,
		hostRepo:      hostRepo,
	}
}

// CreateNodeInvite creates a node join token and returns the full join URL.
func (s *service) CreateNodeInvite(ctx context.Context, adminUserID uint, baseURL string) (link string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	token := hex.EncodeToString(b)

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	t := &nodeentities.NodeJoinToken{
		Token:     token,
		CreatedBy: adminUserID,
		ExpiresAt: expiresAt,
	}
	if err := s.joinTokenRepo.Create(ctx, t); err != nil {
		return "", fmt.Errorf("failed to create join token: %w", err)
	}

	link = baseURL + "/api/v1/nodes/join?token=" + token
	s.logger.Info("Node join token created", "created_by", adminUserID, "token_prefix", token[:8]+"...")
	return link, nil
}

// Join validates the token, upserts the host, creates node credentials, and returns host_id and node_access_token.
func (s *service) Join(ctx context.Context, token string, hostInfo hostentities.HostInfo) (hostID uint, nodeAccessToken string, err error) {
	if token == "" {
		return 0, "", errors.New("join token is required")
	}

	t, err := s.joinTokenRepo.FindByToken(ctx, token)
	if err != nil {
		return 0, "", err
	}
	if t == nil {
		return 0, "", errors.New("invalid or expired join token")
	}

	host, err := s.hostRepo.UpsertHost(ctx, hostInfo)
	if err != nil {
		return 0, "", fmt.Errorf("failed to upsert host: %w", err)
	}

	// Generate node access token (plain, then hash for storage)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return 0, "", fmt.Errorf("failed to generate node token: %w", err)
	}
	nodeAccessToken = hex.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(nodeAccessToken))
	tokenHash := hex.EncodeToString(hash[:])

	if err := s.credRepo.SaveTokenHashForHost(ctx, host.ID, tokenHash); err != nil {
		return 0, "", fmt.Errorf("failed to save node credential: %w", err)
	}

	if err := s.joinTokenRepo.MarkUsed(ctx, t.ID, host.ID); err != nil {
		return 0, "", fmt.Errorf("failed to mark token used: %w", err)
	}

	s.logger.Info("Node joined", "host_id", host.ID, "hostname", host.Name)
	return host.ID, nodeAccessToken, nil
}

// HandlePush updates last_seen and agent_session_started_at (new session if gap > health.AgentPushGapSessionReset).
func (s *service) HandlePush(ctx context.Context, hostID uint) error {
	host, err := s.hostRepo.GetHostByID(ctx, hostID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	gap := now.Sub(host.LastSeen)
	var sessionStart time.Time
	if host.AgentSessionStartedAt == nil || gap > healthapp.AgentPushGapSessionReset {
		sessionStart = now
	} else {
		sessionStart = *host.AgentSessionStartedAt
	}
	return s.hostRepo.UpdateLastSeenAndAgentSession(ctx, hostID, now, &sessionStart)
}

// ValidateNodeToken validates a node access token and returns the host ID.
func (s *service) ValidateNodeToken(ctx context.Context, token string) (hostID uint, err error) {
	if token == "" {
		return 0, errors.New("node token is required")
	}
	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])

	cred, err := s.credRepo.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return 0, err
	}
	if cred == nil {
		return 0, errors.New("invalid node token")
	}
	return cred.HostID, nil
}

func (s *service) RegenerateNodeAccessToken(ctx context.Context, hostID uint) (string, error) {
	if _, err := s.hostRepo.GetHostByID(ctx, hostID); err != nil {
		return "", err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate node token: %w", err)
	}
	plain := hex.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(plain))
	tokenHash := hex.EncodeToString(hash[:])
	if err := s.credRepo.SaveTokenHashForHost(ctx, hostID, tokenHash); err != nil {
		return "", fmt.Errorf("failed to save node credential: %w", err)
	}
	s.logger.Info("Node access token regenerated", "host_id", hostID)
	return plain, nil
}

func (s *service) UpdateAgentClusterConfig(mainNodeURL, nodeAccessToken string) error {
	mainNodeURL = strings.TrimSpace(mainNodeURL)
	nodeAccessToken = strings.TrimSpace(nodeAccessToken)
	mainNodeURL = strings.TrimSuffix(mainNodeURL, "/")
	if mainNodeURL == "" || nodeAccessToken == "" {
		return errors.New("main_node_url and node_access_token are required")
	}
	return clusterconfig.Update(mainNodeURL, nodeAccessToken)
}
