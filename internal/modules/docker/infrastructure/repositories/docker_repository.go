package repositories

import (
	"context"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"

	"system-stats/internal/app/database"
	"system-stats/internal/modules/docker/domain"
	"system-stats/internal/modules/docker/domain/repositories"
	localentities "system-stats/internal/modules/docker/infrastructure/entities"
)

type dockerRepository struct {
	db *gorm.DB
}

func NewDockerRepository(db *gorm.DB) repositories.DockerRepository {
	return &dockerRepository{db: db}
}

func (r *dockerRepository) SaveCurrentMetric(ctx context.Context, metric localentities.DockerMetric, hostId uint) error {
	// Save as historical metric
	timestamp := time.Now().UTC()
	historicalMetric := repositories.HistoricalDockerMetric{
		HostID:            &hostId,
		Timestamp:         timestamp,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}

	// Convert containers from all stacks to entities
	var containerEntities []localentities.DockerContainerEntity
	for _, stack := range metric.Stacks {
		for _, container := range stack.Containers {
			entity, err := container.ToDockerContainerEntity(timestamp)
			if err != nil {
				return err
			}
			containerEntities = append(containerEntities, entity)
		}
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
		Preload("Containers").
		Order("timestamp DESC").
		First(&metric).Error

	if err != nil {
		return localentities.DockerMetric{}, err
	}

	containerMap := make(map[string][]localentities.DockerContainer)
	for _, entity := range metric.Containers {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return localentities.DockerMetric{}, err
		}

		stackName := domain.ExtractStackNameFromContainerName(container.Name)
		containerMap[stackName] = append(containerMap[stackName], container)
	}

	result, err := buildMetricFromHistorical(metric, containerMap)
	if err != nil {
		return localentities.DockerMetric{}, err
	}
	return result, nil
}

func (r *dockerRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*localentities.DockerMetric, error) {
	var metric repositories.HistoricalDockerMetric
	err := r.db.WithContext(ctx).
		Preload("Containers").
		Where("host_id = ?", hostId).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	containerMap := make(map[string][]localentities.DockerContainer)
	for _, entity := range metric.Containers {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return nil, err
		}
		stackName := domain.ExtractStackNameFromContainerName(container.Name)
		containerMap[stackName] = append(containerMap[stackName], container)
	}

	result, err := buildMetricFromHistorical(metric, containerMap)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func buildMetricFromHistorical(metric repositories.HistoricalDockerMetric, containerMap map[string][]localentities.DockerContainer) (localentities.DockerMetric, error) {
	var stacks []localentities.DockerStack
	for stackName, containers := range containerMap {
		sort.Slice(containers, func(i, j int) bool {
			return containers[i].Name < containers[j].Name
		})

		runningContainers := 0
		for _, container := range containers {
			if container.State == "running" {
				runningContainers++
			}
		}

		stacks = append(stacks, localentities.DockerStack{
			Name:              stackName,
			Containers:        containers,
			TotalContainers:   len(containers),
			RunningContainers: runningContainers,
		})
	}

	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	return localentities.DockerMetric{
		Stacks:            stacks,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}, nil
}

func (r *dockerRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]repositories.HistoricalDockerMetric, error) {
	var metrics []repositories.HistoricalDockerMetric
	err := database.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *dockerRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]repositories.HistoricalDockerMetric, error) {
	var metrics []repositories.HistoricalDockerMetric
	err := database.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
