package sensors

import (
	"context"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/sensors/infrastructure/collectors"
	"system-stats/internal/modules/sensors/infrastructure/entities"
)

type Service interface {
	Collect(ctx context.Context) (entities.TemperatureMetric, error)
}

type service struct {
	logger    *log.Logger
	collector *collectors.SensorsCollector
}

func NewService(logger *log.Logger) Service {
	return &service{
		logger:    logger,
		collector: collectors.NewSensorsCollector(logger),
	}
}

func (s *service) Collect(ctx context.Context) (entities.TemperatureMetric, error) {
	return s.collector.CollectTemperatures(ctx)
}
