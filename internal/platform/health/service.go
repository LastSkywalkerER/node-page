package health

import (
	"context"
	"time"

	"github.com/charmbracelet/log"

	hosts "system-stats/internal/cluster/hosts"
	nodes "system-stats/internal/cluster/nodes"
)

type Service interface {
	GetHealth(ctx context.Context, hostID *uint) (*HealthResponse, error)
}

type service struct {
	logger         *log.Logger
	hostRepository hosts.Repository
	nodeCredRepo   nodes.CredentialRepository
	startTime      time.Time
}

// NewService creates a new health service.
func NewService(
	logger *log.Logger,
	hostRepo hosts.Repository,
	nodeCredRepo nodes.CredentialRepository,
	startTime time.Time,
) Service {
	return &service{
		logger:         logger,
		hostRepository: hostRepo,
		nodeCredRepo:   nodeCredRepo,
		startTime:      startTime,
	}
}

func (s *service) GetHealth(ctx context.Context, hostID *uint) (*HealthResponse, error) {
	s.logger.Debug("Getting health information", "host_id", hostID)

	now := time.Now().UTC()
	serverUptime := formatSessionUptime(time.Since(s.startTime))

	if hostID == nil {
		return &HealthResponse{
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

	offlineAfter := hosts.LocalHostOfflineThreshold
	if isAgent {
		offlineAfter = hosts.AgentOfflineThreshold
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

	resp := &HealthResponse{
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
