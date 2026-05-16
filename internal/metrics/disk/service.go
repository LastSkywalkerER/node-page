package disk

import (
	"context"

	appmetrics "system-stats/internal/app/metrics"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (DiskMetric, error)
	Save(ctx context.Context, metric DiskMetric, hostId uint) error
	GetLatest(ctx context.Context) (DiskMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*DiskMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]HistoricalDiskMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDiskMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type service struct {
	appmetrics.Service[DiskMetric, HistoricalDiskMetric]
}

func NewService(logger *log.Logger, repo Repository) Service {
	return &service{
		Service: appmetrics.Service[DiskMetric, HistoricalDiskMetric]{
			Logger:    logger,
			Name:      "disk",
			Collector: newDiskCollector(logger),
			Repo:      repo,
		},
	}
}
