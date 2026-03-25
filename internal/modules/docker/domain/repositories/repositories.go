package repositories

import (
	"context"
	"time"

	localentities "system-stats/internal/modules/docker/infrastructure/entities"
)

type DockerMetricsCollector interface {
	CollectDockerMetrics(ctx context.Context) (localentities.DockerMetric, error)
	IsDockerAvailable(ctx context.Context) bool
	Close() error
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
	HostID            *uint                                  `json:"host_id" gorm:"default:null;index;index:idx_docker_host_ts"`
	Timestamp         time.Time                              `json:"timestamp" gorm:"primaryKey;index;index:idx_docker_host_ts"`
	TotalContainers   int                                    `json:"total_containers" gorm:"column:total_containers"`
	RunningContainers int                                    `json:"running_containers" gorm:"column:running_containers"`
	DockerAvailable   bool                                   `json:"docker_available" gorm:"column:docker_available"`
	Containers        []localentities.DockerContainerEntity  `gorm:"foreignKey:MetricTimestamp"`
}

func (h HistoricalDockerMetric) GetTimestamp() time.Time { return h.Timestamp }
func (h HistoricalDockerMetric) GetMetricType() string   { return "docker" }
func (HistoricalDockerMetric) TableName() string         { return "docker_metrics" }
