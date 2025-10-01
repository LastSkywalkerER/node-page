package repositories

import (
	"context"
	"time"

	localentities "system-stats/internal/modules/docker/infrastructure/entities"
)

/**
 * DockerMetricsCollector defines the interface for collecting Docker container metrics.
 * This interface provides methods to gather information about Docker daemon status
 * and individual container performance statistics.
 */
type DockerMetricsCollector interface {
	/**
	 * CollectDockerMetrics gathers Docker container statistics and status information.
	 * @param ctx The context for the operation, used for cancellation and timeouts
	 * @return entities.DockerMetric The collected Docker metrics data
	 * @return error Returns an error if Docker metrics collection fails
	 */
	CollectDockerMetrics(ctx context.Context) (localentities.DockerMetric, error)

	/**
	 * IsDockerAvailable checks if the Docker daemon is accessible and running.
	 * @param ctx The context for the operation, used for cancellation
	 * @return bool Returns true if Docker is available, false otherwise
	 */
	IsDockerAvailable(ctx context.Context) bool
}

/**
 * DockerRepository defines the interface for Docker metric data operations.
 * This repository handles database operations related to storing and retrieving
 * Docker container metrics.
 */
type DockerRepository interface {
	/**
	 * SaveCurrentMetric persists current Docker metrics to the database.
	 * @param ctx The context for the operation, used for cancellation and timeouts
	 * @param metric The Docker metrics data to be stored
	 * @return error Returns an error if the save operation fails
	 */
	SaveCurrentMetric(ctx context.Context, metric localentities.DockerMetric) error

	/**
	 * GetLatestMetric retrieves the most recent Docker metrics from the database.
	 * @param ctx The context for the operation, used for cancellation and timeouts
	 * @return entities.DockerMetric The latest Docker metrics data
	 * @return error Returns an error if the retrieval operation fails
	 */
	GetLatestMetric(ctx context.Context) (localentities.DockerMetric, error)

	/**
	 * GetHistoricalMetrics retrieves historical Docker metrics for the specified time period.
	 * @param ctx The context for the operation, used for cancellation and timeouts
	 * @param hours The number of hours of historical data to retrieve
	 * @return []interface{} Array of historical Docker metrics
	 * @return error Returns an error if the retrieval operation fails
	 */
	GetHistoricalMetrics(ctx context.Context, hours float64) ([]interface{}, error)
}

/**
 * HistoricalDockerMetric represents a historical Docker daemon metric stored in the database.
 * This structure contains Docker container statistics recorded at a specific time,
 * including container counts and Docker availability status.
 */
type HistoricalDockerMetric struct {
	/** Timestamp indicates when this Docker metric was recorded (primary key) */
	Timestamp time.Time `json:"timestamp" gorm:"primaryKey"`

	/** TotalContainers shows the total number of containers at the time of recording */
	TotalContainers int `json:"total_containers" gorm:"column:total_containers"`

	/** RunningContainers shows the number of running containers at the time of recording */
	RunningContainers int `json:"running_containers" gorm:"column:running_containers"`

	/** DockerAvailable indicates whether Docker daemon was accessible at the time of recording */
	DockerAvailable bool `json:"docker_available" gorm:"column:docker_available"`

	/** Containers contains detailed information about each Docker container at the time of recording */
	Containers []localentities.DockerContainerEntity `gorm:"foreignKey:MetricTimestamp"`
}

/**
 * GetTimestamp returns the timestamp when this Docker metric was recorded.
 * @return time.Time The recording timestamp
 */
func (h HistoricalDockerMetric) GetTimestamp() time.Time { return h.Timestamp }

/**
 * GetMetricType returns the metric type identifier for Docker metrics.
 * @return string Always returns "docker"
 */
func (h HistoricalDockerMetric) GetMetricType() string { return "docker" }

/**
 * TableName returns the database table name for GORM operations.
 * @return string The table name "docker_metrics"
 */
func (HistoricalDockerMetric) TableName() string { return "docker_metrics" }
