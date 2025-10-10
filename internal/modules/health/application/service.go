package health

import (
	"context"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/health/infrastructure/entities"
	hostrepos "system-stats/internal/modules/hosts/infrastructure/repositories"
)

type Service interface {
	GetHealth(ctx context.Context, hostID *uint) (*entities.HealthResponse, error)
}

type service struct {
	logger         *log.Logger
	hostRepository hostrepos.HostRepository
	startTime      time.Time
}

func NewService(logger *log.Logger, hostRepository hostrepos.HostRepository, startTime time.Time) Service {
	return &service{
		logger:         logger,
		hostRepository: hostRepository,
		startTime:      startTime,
	}
}

func (s *service) GetHealth(ctx context.Context, hostID *uint) (*entities.HealthResponse, error) {
	s.logger.Info("Getting health information", "host_id", hostID)

	now := time.Now().UTC()
	serverUptime := time.Since(s.startTime).String()

	// If no host ID provided, return server health
	if hostID == nil {
		return &entities.HealthResponse{
			Status:    "ok",
			Timestamp: now,
			Uptime:    serverUptime,
		}, nil
	}

	// Get host information
	host, err := s.hostRepository.GetHostByID(ctx, *hostID)
	if err != nil {
		s.logger.Error("Failed to get host by ID", "error", err, "host_id", *hostID)
		return nil, err
	}

	// Calculate host health
	timeSinceLastSeen := now.Sub(host.LastSeen).Seconds()

	var status string
	var latency float64

	// Consider host online if last seen within last 5 minutes
	if timeSinceLastSeen < 300 {
		status = "online"
		latency = 0.0 // Local host, no network latency
	} else {
		status = "offline"
		latency = -1.0 // Unknown latency for offline hosts
	}

	// Calculate uptime as time since last seen (in seconds)
	uptime := int64(timeSinceLastSeen)

	health := &entities.HealthResponse{
		Status:     status,
		Timestamp:  now,
		Uptime:     serverUptime,
		HostID:     host.ID,
		Latency:    latency,
		HostUptime: uptime,
		LastSeen:   host.LastSeen,
	}

	s.logger.Info("Health information retrieved", "host_id", hostID, "status", status, "uptime", uptime)
	return health, nil
}
