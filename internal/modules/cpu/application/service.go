package cpumetrics

import (
	"context"

	"system-stats/internal/app/metrics"
	"system-stats/internal/modules/cpu/infrastructure/collectors"
	"system-stats/internal/modules/cpu/infrastructure/entities"
	cpurepos "system-stats/internal/modules/cpu/infrastructure/repositories"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.CPUMetric, error)
	Save(ctx context.Context, metric entities.CPUMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.CPUMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.CPUMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalCPUMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalCPUMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type cpuCollectorAdapter struct {
	c *collectors.CPUMetricsCollector
}

func (a *cpuCollectorAdapter) Collect(ctx context.Context) (entities.CPUMetric, error) {
	return a.c.CollectCPUMetrics(ctx)
}

type service struct {
	metrics.Service[entities.CPUMetric, entities.HistoricalCPUMetric]
}

func NewService(logger *log.Logger, cpuRepository cpurepos.CPURepository) Service {
	return &service{
		Service: metrics.Service[entities.CPUMetric, entities.HistoricalCPUMetric]{
			Logger:    logger,
			Name:      "cpu",
			Collector: &cpuCollectorAdapter{c: collectors.NewCPUMetricsCollector(logger)},
			Repo:      cpuRepository,
		},
	}
}
