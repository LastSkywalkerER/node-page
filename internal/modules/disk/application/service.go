package diskmetrics

import (
	"context"

	"system-stats/internal/app/metrics"
	"system-stats/internal/modules/disk/infrastructure/collectors"
	"system-stats/internal/modules/disk/infrastructure/entities"
	diskrepos "system-stats/internal/modules/disk/infrastructure/repositories"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (entities.DiskMetric, error)
	Save(ctx context.Context, metric entities.DiskMetric, hostId uint) error
	GetLatest(ctx context.Context) (entities.DiskMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*entities.DiskMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]entities.HistoricalDiskMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]entities.HistoricalDiskMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type diskCollectorAdapter struct {
	c *collectors.DiskMetricsCollector
}

func (a *diskCollectorAdapter) Collect(ctx context.Context) (entities.DiskMetric, error) {
	return a.c.CollectDiskMetrics(ctx)
}

type service struct {
	metrics.Service[entities.DiskMetric, entities.HistoricalDiskMetric]
}

func NewService(logger *log.Logger, diskRepository diskrepos.DiskRepository) Service {
	return &service{
		Service: metrics.Service[entities.DiskMetric, entities.HistoricalDiskMetric]{
			Logger:    logger,
			Name:      "disk",
			Collector: &diskCollectorAdapter{c: collectors.NewDiskMetricsCollector(logger)},
			Repo:      diskRepository,
		},
	}
}
