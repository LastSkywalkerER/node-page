package rammetrics

import (
	"context"

	"system-stats/internal/app/metrics"
	"system-stats/internal/modules/memory/infrastructure/collectors"
	"system-stats/internal/modules/memory/infrastructure/entities"
	memoryrepos "system-stats/internal/modules/memory/infrastructure/repositories"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.MemoryMetric, error)
	Save(ctx context.Context, metric entities.MemoryMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.MemoryMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.MemoryMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalMemoryMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalMemoryMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type memoryCollectorAdapter struct {
	c *collectors.MemoryMetricsCollector
}

func (a *memoryCollectorAdapter) Collect(ctx context.Context) (entities.MemoryMetric, error) {
	return a.c.CollectMemoryMetrics(ctx)
}

type service struct {
	metrics.Service[entities.MemoryMetric, entities.HistoricalMemoryMetric]
}

// GetLatest collects fresh metrics to ensure all current fields are populated.
func (s *service) GetLatest(ctx context.Context) (entities.MemoryMetric, error) {
	return s.Collect(ctx)
}

func NewService(logger *log.Logger, memoryRepository memoryrepos.MemoryRepository) Service {
	return &service{
		Service: metrics.Service[entities.MemoryMetric, entities.HistoricalMemoryMetric]{
			Logger:    logger,
			Name:      "memory",
			Collector: &memoryCollectorAdapter{c: collectors.NewMemoryMetricsCollector(logger)},
			Repo:      memoryRepository,
		},
	}
}
