package docker

import (
	"context"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"system-stats/internal/app/dbutil"
)

type dockerRepository struct {
	db *gorm.DB
}

// NewRepository creates a new Docker repository.
func NewRepository(db *gorm.DB) DockerRepository {
	return &dockerRepository{db: db}
}

func (r *dockerRepository) SaveCurrentMetric(ctx context.Context, metric DockerMetric, hostId uint) error {
	return r.SaveCurrentMetricAt(ctx, metric, hostId, time.Now().UTC())
}

func (r *dockerRepository) SaveCurrentMetricAt(ctx context.Context, metric DockerMetric, hostId uint, ts time.Time) error {
	// Save as historical metric
	timestamp := ts.UTC()
	historicalMetric := HistoricalDockerMetric{
		HostID:            &hostId,
		Timestamp:         timestamp,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}

	// Convert containers from all stacks to entities
	var containerEntities []DockerContainerEntity
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
		// Raft log replay re-applies entries after a restart: the same
		// (host_id, timestamp) row must be a no-op, not a pkey violation.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&historicalMetric).Error; err != nil {
			return err
		}
		if len(containerEntities) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&containerEntities).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *dockerRepository) GetLatestMetric(ctx context.Context) (DockerMetric, error) {
	var metric HistoricalDockerMetric

	err := r.db.WithContext(ctx).
		Preload("Containers").
		Order("timestamp DESC").
		First(&metric).Error

	if err != nil {
		return DockerMetric{}, err
	}

	containerMap := make(map[string][]DockerContainer)
	for _, entity := range metric.Containers {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return DockerMetric{}, err
		}

		stackName := ExtractStackNameFromContainerName(container.Name)
		containerMap[stackName] = append(containerMap[stackName], container)
	}

	result, err := buildMetricFromHistorical(metric, containerMap)
	if err != nil {
		return DockerMetric{}, err
	}
	return result, nil
}

func (r *dockerRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*DockerMetric, error) {
	var metric HistoricalDockerMetric
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
	containerMap := make(map[string][]DockerContainer)
	for _, entity := range metric.Containers {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return nil, err
		}
		stackName := ExtractStackNameFromContainerName(container.Name)
		containerMap[stackName] = append(containerMap[stackName], container)
	}

	result, err := buildMetricFromHistorical(metric, containerMap)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func buildMetricFromHistorical(metric HistoricalDockerMetric, containerMap map[string][]DockerContainer) (DockerMetric, error) {
	var stacks []DockerStack
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

		stacks = append(stacks, DockerStack{
			Name:              stackName,
			Containers:        containers,
			TotalContainers:   len(containers),
			RunningContainers: runningContainers,
		})
	}

	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	return DockerMetric{
		Stacks:            stacks,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}, nil
}

func (r *dockerRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalDockerMetric, error) {
	var metrics []HistoricalDockerMetric
	err := dbutil.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *dockerRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDockerMetric, error) {
	var metrics []HistoricalDockerMetric
	err := dbutil.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
