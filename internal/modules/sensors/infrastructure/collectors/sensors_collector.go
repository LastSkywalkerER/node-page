package collectors

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/sensors"

	"system-stats/internal/modules/sensors/infrastructure/entities"
)

type SensorsCollector struct {
	logger *log.Logger
}

func NewSensorsCollector(logger *log.Logger) *SensorsCollector {
	return &SensorsCollector{logger: logger}
}

func (c *SensorsCollector) CollectTemperatures(ctx context.Context) (entities.TemperatureMetric, error) {
	c.logger.Info("Collecting temperature sensors")
	temps, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect temperatures with context, trying fallback", "error", err)
		temps, err = sensors.SensorsTemperatures()
		if err != nil {
			c.logger.Error("Failed to collect temperatures", "error", err)
			return entities.TemperatureMetric{Timestamp: time.Now(), Sensors: []entities.TemperatureStat{}}, nil
		}
	}

	out := make([]entities.TemperatureStat, 0, len(temps))
	for _, t := range temps {
		out = append(out, entities.TemperatureStat{
			SensorKey:   t.SensorKey,
			Temperature: t.Temperature,
			High:        t.High,
			Critical:    t.Critical,
		})
	}
	c.logger.Info("Collected sensors", "count", len(out))
	return entities.TemperatureMetric{Timestamp: time.Now(), Sensors: out}, nil
}
