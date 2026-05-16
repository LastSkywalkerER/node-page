package sensors

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/sensors"
)

type sensorsCollector struct {
	logger *log.Logger
}

func newSensorsCollector(logger *log.Logger) *sensorsCollector {
	return &sensorsCollector{logger: logger}
}

func (c *sensorsCollector) Collect(ctx context.Context) (TemperatureMetric, error) {
	c.logger.Debug("Collecting temperature sensors")
	temps, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect temperatures with context, trying fallback", "error", err)
		temps, err = sensors.SensorsTemperatures()
		if err != nil {
			c.logger.Error("Failed to collect temperatures", "error", err)
			return TemperatureMetric{Timestamp: time.Now(), Sensors: []TemperatureStat{}}, nil
		}
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
