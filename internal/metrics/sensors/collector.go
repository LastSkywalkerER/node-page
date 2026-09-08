package sensors

import (
	"context"
	"time"

	"github.com/charmbracelet/log"

	"system-stats/internal/platform/hwsensors"
)

type sensorsCollector struct {
	logger *log.Logger
}

func newSensorsCollector(logger *log.Logger) *sensorsCollector {
	return &sensorsCollector{logger: logger}
}

// Collect returns the current temperature sensors through the shared reader
// (one cached, time-bounded read per tick, shared with the cpu collector).
func (c *sensorsCollector) Collect(ctx context.Context) (TemperatureMetric, error) {
	c.logger.Debug("Collecting temperature sensors")
	temps, err := hwsensors.Temperatures(ctx)
	if err != nil {
		c.logger.Debug("No temperature sensors available", "error", err)
		return TemperatureMetric{Timestamp: time.Now(), Sensors: []TemperatureStat{}}, nil
	}

	out := make([]TemperatureStat, 0, len(temps))
	for _, t := range temps {
		out = append(out, TemperatureStat{
			SensorKey:   t.SensorKey,
			Temperature: t.Temperature,
			High:        t.High,
			Critical:    t.Critical,
		})
	}
	c.logger.Debug("Collected sensors", "count", len(out))
	return TemperatureMetric{Timestamp: time.Now(), Sensors: out}, nil
}
