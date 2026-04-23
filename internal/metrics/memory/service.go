package memory

import (
	"context"

	appmetrics "system-stats/internal/app/metrics"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (MemoryMetric, error)
	Save(ctx context.Context, metric MemoryMetric, hostId uint) error
	GetLatest(ctx context.Context) (MemoryMetric, error)
	GetLatestByHost(ctx context.Context, hostId uint) (*MemoryMetric, error)
	GetHistorical(ctx context.Context, hours float64) ([]HistoricalMemoryMetric, error)
	GetHistoricalByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalMemoryMetric, error)
	CollectAndSave(ctx context.Context, hostId uint) error
}

type service struct {
	appmetrics.Service[MemoryMetric, HistoricalMemoryMetric]
}

// GetLatest always collects fresh to populate all fields.
func (s *service) GetLatest(ctx context.Context) (MemoryMetric, error) {
	return s.Collect(ctx)
}

func NewService(logger *log.Logger, repo Repository) Service {
	return &service{
		Service: appmetrics.Service[MemoryMetric, HistoricalMemoryMetric]{
			Logger:    logger,
			Name:      "memory",
			Collector: newMemoryCollector(logger),
			Repo:      repo,
		},
	}
}
