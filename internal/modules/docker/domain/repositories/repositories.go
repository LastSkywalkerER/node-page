package repositories

import (
	"context"
	"time"

	localentities "system-stats/internal/modules/docker/infrastructure/entities"
)

// DockerMetricsCollector defines the interface for collecting Docker container metrics.
type DockerMetricsCollector interface {
	// CollectDockerMetrics gathers Docker container statistics and status information.
	CollectDockerMetrics(ctx context.Context) (localentities.DockerMetric, error)

	// IsDockerAvailable checks if the Docker daemon is accessible and running.
	IsDockerAvailable(ctx context.Context) bool
}

// DockerRepository defines the interface for Docker metric data operations.
type DockerRepository interface {
	SaveCurrentMetric(ctx context.Context, metric localentities.DockerMetric, hostId uint) error
	GetLatestMetric(ctx context.Context) (localentities.DockerMetric, error)
	GetLatestMetricByHost(ctx context.Context, hostId uint) (*localentities.DockerMetric, error)
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalDockerMetric, error)
	GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDockerMetric, error)
}

// HistoricalDockerMetric represents a historical Docker daemon metric stored in the database.
type HistoricalDockerMetric struct {
	HostID            *uint                                  `json:"host_id" gorm:"default:null"`
	Timestamp         time.Time                              `json:"timestamp" gorm:"primaryKey"`
	TotalContainers   int                                    `json:"total_containers" gorm:"column:total_containers"`
	RunningContainers int                                    `json:"running_containers" gorm:"column:running_containers"`
	DockerAvailable   bool                                   `json:"docker_available" gorm:"column:docker_available"`
	Containers        []localentities.DockerContainerEntity  `gorm:"foreignKey:MetricTimestamp"`
}

func (h HistoricalDockerMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalDockerMetric) GetMetricType() string   { return "docker" }
func (HistoricalDockerMetric) TableName() string         { return "docker_metrics" }
