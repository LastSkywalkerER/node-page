package repositories

import (
	"context"
	"sort"
	"strconv"
	"strings"
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

	// Convert container entities to containers and group by stacks
	containerMap := make(map[string][]localentities.DockerContainer)
	for _, entity := range metric.Containers {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return localentities.DockerMetric{}, err
		}

		// For now, we need to determine stack from container name or labels
		// Since we don't store stack info in DB yet, we'll group by container name prefix
		stackName := r.extractStackNameFromContainerName(container.Name)
		stackName = r.normalizeStackName(stackName)

		containerMap[stackName] = append(containerMap[stackName], container)
	}

	// Convert map to stacks
	var stacks []localentities.DockerStack
	for stackName, containers := range containerMap {
		// Sort containers within stack by name
		sort.Slice(containers, func(i, j int) bool {
			return containers[i].Name < containers[j].Name
		})

		totalContainers := len(containers)
		runningContainers := 0
		for _, container := range containers {
			if container.State == "running" {
				runningContainers++
			}
		}

		stacks = append(stacks, localentities.DockerStack{
			Name:              stackName,
			Containers:        containers,
			TotalContainers:   totalContainers,
			RunningContainers: runningContainers,
		})
	}

	// Sort stacks by name
	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	// Convert historical to current metric
	return localentities.DockerMetric{
		Stacks:            stacks,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}, nil
}

// extractStackNameFromContainerName attempts to extract stack name from container name
// For containers created by docker-compose, the name typically follows pattern: project-service-instance
func (r *dockerRepository) extractStackNameFromContainerName(containerName string) string {
	if containerName == "" {
		return containerName
	}

	// Handle special cases first
	if containerName == "nocodb" {
		return "noco-db"
	}

	// Handle buildx case
	if strings.HasPrefix(containerName, "buildx_") {
		return "buildx"
	}

	// Split by hyphens
	parts := strings.Split(containerName, "-")

	// If no hyphens or only one part, return as is (standalone container)
	if len(parts) < 2 {
		return containerName
	}

	// Check if last part is a number (instance number like -1, -2, etc)
	if _, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
		// Has number at the end, pattern like: stack-service-1
		// Take everything except the last part (instance number)
		if len(parts) >= 3 {
			return strings.Join(parts[:len(parts)-2], "-")
		}
		// If only 2 parts and last is number, take first part
		return parts[0]
	}

	// No number at end, check for service suffixes
	serviceSuffixes := []string{"redis", "postgres", "kafka", "zookeeper", "db"}
	lastPart := parts[len(parts)-1]
	for _, suffix := range serviceSuffixes {
		if lastPart == suffix {
			// Pattern like: stack-service
			if len(parts) >= 2 {
				return strings.Join(parts[:len(parts)-1], "-")
			}
		}
	}

	// If no pattern matches, return first part
	return parts[0]
}

// normalizeStackName normalizes stack names to handle edge cases
func (r *dockerRepository) normalizeStackName(stackName string) string {
	switch stackName {
	case "nocodb":
		return "noco-db"
	case "noco-db-postgres", "noco-db-redis":
		return "noco-db"
	case "multi-sig-backend-postgres-1", "multi-sig-backend-redis-1":
		return "multi-sig-backend"
	case "haust-local-kafka-1", "haust-local-redis-1", "haust-local-zookeeper-1", "haust-local-mdata-db-1", "haust-local-wallet-db-1":
		return "haust-local"
	case "haust-local-mdata", "haust-local-wallet":
		return "haust-local"
	default:
		return stackName
	}
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

func (r *dockerRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]interface{}, error) {
	var metrics []repositories.HistoricalDockerMetric

	query := r.db.WithContext(ctx).
		Where("host_id = ? AND timestamp >= datetime('now', '-' || ? || ' hours')", hostId, hours).
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
