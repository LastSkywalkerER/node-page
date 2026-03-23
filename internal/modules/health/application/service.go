package health

import (
	"context"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/health/infrastructure/entities"
	hostrepos "system-stats/internal/modules/hosts/infrastructure/repositories"
	noderepos "system-stats/internal/modules/nodes/infrastructure/repositories"
)

const (
	// AgentPushGapSessionReset starts a new agent session if last push was longer ago than this.
	AgentPushGapSessionReset = 30 * time.Second
	// AgentOfflineThreshold marks a cluster agent offline if last push is older than this.
	AgentOfflineThreshold = 45 * time.Second
	// LocalHostOfflineThreshold for hosts without node credentials (local collector only).
	LocalHostOfflineThreshold = 5 * time.Minute
)

type Service interface {
	GetHealth(ctx context.Context, hostID *uint) (*entities.HealthResponse, error)
}

type service struct {
	logger         *log.Logger
	hostRepository hostrepos.HostRepository
	nodeCredRepo   noderepos.NodeCredentialRepository
	startTime      time.Time
}

func NewService(
	logger *log.Logger,
	hostRepository hostrepos.HostRepository,
	nodeCredRepo noderepos.NodeCredentialRepository,
	startTime time.Time,
) Service {
	return &service{
		logger:         logger,
		hostRepository: hostRepository,
		nodeCredRepo:   nodeCredRepo,
		startTime:      startTime,
	}
}

func (s *service) GetHealth(ctx context.Context, hostID *uint) (*entities.HealthResponse, error) {
	s.logger.Debug("Getting health information", "host_id", hostID)

	now := time.Now().UTC()
	serverUptime := formatSessionUptime(time.Since(s.startTime))

	if hostID == nil {
		return &entities.HealthResponse{
			Status:    "ok",
			Timestamp: now,
			Uptime:    serverUptime,
		}, nil
	}

	host, err := s.hostRepository.GetHostByID(ctx, *hostID)
	if err != nil {
		s.logger.Error("Failed to get host by ID", "error", err, "host_id", *hostID)
		return nil, err
	}

	timeSinceLastSeen := now.Sub(host.LastSeen)

	cred, err := s.nodeCredRepo.FindByHostID(ctx, host.ID)
	if err != nil {
		s.logger.Error("Failed to look up node credential", "error", err, "host_id", host.ID)
		return nil, err
	}
	isAgent := cred != nil

	offlineAfter := LocalHostOfflineThreshold
	if isAgent {
		offlineAfter = AgentOfflineThreshold
	}

	var status string
	var latency float64
	if timeSinceLastSeen < offlineAfter {
		status = "online"
		latency = 0.0
	} else {
		status = "offline"
		latency = -1.0
	}

	// Seconds since last activity (for debugging / legacy clients)
	sinceLast := int64(timeSinceLastSeen.Seconds())

	resp := &entities.HealthResponse{
		Status:         status,
		Timestamp:      now,
		Uptime:         serverUptime,
		HostID:         host.ID,
		Latency:        latency,
		HostUptime:     sinceLast,
		LastSeen:       host.LastSeen,
		IsClusterAgent: isAgent,
	}

	if isAgent && status == "online" && host.AgentSessionStartedAt != nil {
		sessionDur := now.Sub(*host.AgentSessionStartedAt)
		resp.SessionUptime = formatSessionUptime(sessionDur)
		resp.HostUptime = int64(sessionDur.Seconds())
	} else if isAgent {
		// Agent but offline or session unknown — no session uptime string
		resp.SessionUptime = ""
		resp.HostUptime = 0
	} else {
		// Local collector host: no push-based session in DB
		resp.SessionUptime = ""
		resp.HostUptime = 0
	}

	s.logger.Debug("Health information retrieved", "host_id", hostID, "status", status, "is_agent", isAgent)
	return resp, nil
}
