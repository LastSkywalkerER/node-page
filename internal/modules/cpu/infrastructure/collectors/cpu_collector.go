package collectors

import (
	"context"
	"runtime"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/sensors"

	"system-stats/internal/modules/cpu/infrastructure/entities"
)

/**
 * cpuMetricsCollector implements the CPUMetricsCollector interface.
 * This collector gathers CPU performance statistics using cross-platform
 * system monitoring libraries (gopsutil).
 */
type CPUMetricsCollector struct {
	logger *log.Logger
}

/**
 * NewCPUMetricsCollector creates a new CPU metrics collector instance.
 * This constructor initializes the collector for gathering CPU statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *cpuMetricsCollector Returns the initialized CPU collector
 */
func NewCPUMetricsCollector(logger *log.Logger) *CPUMetricsCollector {
	return &CPUMetricsCollector{logger: logger}
}

/**
 * CollectCPUMetrics gathers current CPU performance statistics.
 * This method collects CPU usage percentage, core count, and system load averages.
 *
 * @param ctx The context for the operation
 * @return entities.CPUMetric The collected CPU metrics
 * @return error Returns an error if CPU metrics collection fails
 */
func (c *CPUMetricsCollector) CollectCPUMetrics(ctx context.Context) (entities.CPUMetric, error) {
	c.logger.Info("Collecting CPU usage percentage")
	// Get CPU usage percentage (without delay for fast response)
	percentages, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		c.logger.Error("Failed to collect CPU usage percentage", "error", err)
		return entities.CPUMetric{}, err
	}

	var usage float64
	if len(percentages) > 0 {
		usage = percentages[0]
	}

	// Get number of cores
	cores := runtime.NumCPU()

	c.logger.Info("Collecting CPU load averages")
	// Get load average
	loadStat, err := load.AvgWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect CPU load averages", "error", err)
		return entities.CPUMetric{}, err
	}

	c.logger.Info("Collecting CPU temperature")
	// Get CPU temperature
	temperature := 0.0
	temperatures, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect CPU temperature with context, trying SensorsTemperatures", "error", err)
		// Try SensorsTemperatures as fallback
		temperatures, err = sensors.SensorsTemperatures()
	}

	if err != nil {
		c.logger.Warn("Failed to collect CPU temperature, using 0.0", "error", err)
	} else {
		c.logger.Info("Found temperature sensors", "count", len(temperatures))

		// Extended list of CPU temperature sensor keys for different OS
		cpuSensorKeys := []string{
			// Linux sensors
			"coretemp", "k10temp", "k8temp", "cpu_thermal", "acpitz", "thermal_zone0",
			"cpu", "core", "processor", "cpu0", "cpu1", "cpu2", "cpu3",
			// macOS sensors (SMC keys)
			"cpu_thermal", "cpu_core", "cpu_die", "cpu_proximity",
			"TC0P", "TC0D", "TC0H", "TG0P", "TG0D", "TG0H", "TH0P",
			"TM0P", "TM0S", "TN0P", "TN0D", "TN0H", "TI0P", "TI1P",
			"TA0P", "TA1P", "TW0P",
			// Battery sensors (can be used as system temperature indicator)
			"TB0T", "TB1T", "TB2T", "TB3T",
			// Generic thermal sensors
			"thermal", "temp", "temperature",
		}

		// Look for CPU temperature sensor
		for _, temp := range temperatures {
			sensorKeyLower := strings.ToLower(temp.SensorKey)
			for _, key := range cpuSensorKeys {
				if strings.Contains(sensorKeyLower, strings.ToLower(key)) {
					temperature = temp.Temperature
					c.logger.Info("Found CPU temperature sensor", "key", temp.SensorKey, "temperature", temperature)
					break
				}
			}
			if temperature > 0 {
				break
			}
		}

		// If no specific CPU sensor found, try to find any thermal sensor
		if temperature == 0.0 {
			for _, temp := range temperatures {
				sensorKeyLower := strings.ToLower(temp.SensorKey)
				if strings.Contains(sensorKeyLower, "thermal") ||
					strings.Contains(sensorKeyLower, "temp") ||
					strings.Contains(sensorKeyLower, "cpu") ||
					strings.Contains(sensorKeyLower, "core") {
					temperature = temp.Temperature
					c.logger.Info("Using thermal sensor as fallback", "key", temp.SensorKey, "temperature", temperature)
					break
				}
			}
		}

		// macOS specific: if still no temperature, try battery sensors as system indicator
		if temperature == 0.0 {
			for _, temp := range temperatures {
				if (temp.SensorKey == "TB0T" || temp.SensorKey == "TB1T") && temp.Temperature > 0 {
					temperature = temp.Temperature
					c.logger.Info("Using battery sensor as system temperature indicator", "key", temp.SensorKey, "temperature", temperature)
					break
				}
			}
		}

		// Last resort: use the first available temperature if it's reasonable (> 0 and < 150)
		if temperature == 0.0 && len(temperatures) > 0 {
			firstTemp := temperatures[0].Temperature
			if firstTemp > 0 && firstTemp < 150 {
				temperature = firstTemp
				c.logger.Info("Using first available temperature sensor", "key", temperatures[0].SensorKey, "temperature", temperature)
			}
		}
	}

	c.logger.Info("CPU metrics collected successfully", "usage_percent", usage, "cores", cores, "temperature", temperature)
	return entities.CPUMetric{
		UsagePercent: usage,
		Cores:        cores,
		LoadAvg1:     loadStat.Load1,
		LoadAvg5:     loadStat.Load5,
		LoadAvg15:    loadStat.Load15,
		Temperature:  temperature,
	}, nil
}
