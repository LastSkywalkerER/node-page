package repositories

import (
	"context"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/modules/docker/domain/repositories"
	localentities "system-stats/internal/modules/docker/infrastructure/entities"
)

type dockerRepository struct {
	db *gorm.DB
}

func NewDockerRepository(db *gorm.DB) repositories.DockerRepository {
	db.AutoMigrate(&localentities.DockerContainerEntity{})
	db.AutoMigrate(&repositories.HistoricalDockerMetric{})
	return &dockerRepository{db: db}
}

func (r *dockerRepository) SaveCurrentMetric(ctx context.Context, metric localentities.DockerMetric) error {
	// Save as historical metric
	timestamp := time.Now().UTC()
	historicalMetric := repositories.HistoricalDockerMetric{
		Timestamp:         timestamp,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}

	// Convert containers to entities
	containerEntities := make([]localentities.DockerContainerEntity, len(metric.Containers))
	for i, container := range metric.Containers {
		entity, err := container.ToDockerContainerEntity(timestamp)
		if err != nil {
			return err
		}
		containerEntities[i] = entity
	}

	// Save metric and containers in transaction
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&historicalMetric).Error; err != nil {
			return err
		}
		if len(containerEntities) > 0 {
			if err := tx.Create(&containerEntities).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *dockerRepository) GetLatestMetric(ctx context.Context) (localentities.DockerMetric, error) {
	var metric repositories.HistoricalDockerMetric

	err := r.db.WithContext(ctx).
		Preload("Containers", func(db *gorm.DB) *gorm.DB {
			return db.Order("cpu_percent_of_limit DESC")
		}).
		Order("timestamp DESC").
		First(&metric).Error

	if err != nil {
		return localentities.DockerMetric{}, err
	}

	// Convert container entities to containers
	containers := make([]localentities.DockerContainer, len(metric.Containers))
	for i, entity := range metric.Containers {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return localentities.DockerMetric{}, err
		}
		containers[i] = container
	}

	// Convert historical to current metric
	return localentities.DockerMetric{
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
		Containers:        containers,
	}, nil
}

func (r *dockerRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]interface{}, error) {
	var metrics []repositories.HistoricalDockerMetric

	query := r.db.WithContext(ctx).
		Where("timestamp >= datetime('now', '-' || ? || ' hours')", hours).
		Order("timestamp ASC").
		Find(&metrics)

	if query.Error != nil {
		return nil, query.Error
	}

	// Convert to []interface{} for compatibility
	result := make([]interface{}, len(metrics))
	for i, metric := range metrics {
		result[i] = metric
	}
	return result, nil
}
