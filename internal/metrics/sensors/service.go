package sensors

import (
	"context"

	"github.com/charmbracelet/log"
)

type Service interface {
	Collect(ctx context.Context) (TemperatureMetric, error)
}

type service struct {
	logger    *log.Logger
	collector *sensorsCollector
}

func NewService(logger *log.Logger) Service {
	return &service{
		logger:    logger,
		collector: newSensorsCollector(logger),
	}
}

func (s *service) Collect(ctx context.Context) (TemperatureMetric, error) {
	return s.collector.Collect(ctx)
}
