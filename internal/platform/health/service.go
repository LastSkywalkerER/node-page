package health

import (
	"context"
	"time"

	"github.com/charmbracelet/log"

	hosts "system-stats/internal/cluster/hosts"
)

type Service interface {
	GetHealth(ctx context.Context, hostID *uint) (*HealthResponse, error)
}

type service struct {
	logger         *log.Logger
	hostRepository hosts.Repository
	startTime      time.Time
}

// NewService creates a new health service.
func NewService(
	logger *log.Logger,
	hostRepo hosts.Repository,
	startTime time.Time,
) Service {
	return &service{
		logger:         logger,
		hostRepository: hostRepo,
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

	// A host is online when its last_seen is fresh. Every node's local collector
	// refreshes last_seen each metrics cycle (~5s) and Raft replicates it
	// cluster-wide, so one threshold covers both the local collector and remote
	// Raft peers.
	timeSinceLastSeen := now.Sub(host.LastSeen)
	var status string
	var latency float64
	if timeSinceLastSeen < hosts.HostOfflineThreshold {
		status = "online"
		latency = 0.0
	} else {
		status = "offline"
		latency = -1.0
	}

	resp := &HealthResponse{
		Status:     status,
		Timestamp:  now,
		Uptime:     serverUptime,
		HostID:     host.ID,
		Latency:    latency,
		HostUptime: int64(timeSinceLastSeen.Seconds()),
		LastSeen:   host.LastSeen,
	}

	s.logger.Debug("Health information retrieved", "host_id", hostID, "status", status)
	return resp, nil
}
