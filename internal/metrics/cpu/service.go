package cpu

import (
	"context"

	appmetrics "system-stats/internal/app/metrics"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (CPUMetric, error)
	Save(ctx context.Context, metric CPUMetric, hostId uint) error
	GetLatest(ctx context.Context) (CPUMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*CPUMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]HistoricalCPUMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalCPUMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type service struct {
	appmetrics.Service[CPUMetric, HistoricalCPUMetric]
}

func NewService(logger *log.Logger, repo Repository) Service {
	return &service{
		Service: appmetrics.Service[CPUMetric, HistoricalCPUMetric]{
			Logger:    logger,
			Name:      "cpu",
			Collector: newCPUCollector(logger),
			Repo:      repo,
		},
	}
}
