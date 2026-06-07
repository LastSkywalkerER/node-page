package cpu

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/sensors"
)

// cpuUsageMinInterval is the shortest window over which a fresh CPU% sample is
// meaningful. gopsutil computes usage as the busy fraction SINCE the previous
// call, so two calls a few milliseconds apart (the per-cycle DB save and the
// SSE/replication snapshot) would make the second one garbage. Within this
// window we return the cached value and leave gopsutil's baseline untouched.
const cpuUsageMinInterval = 3 * time.Second

type cpuCollector struct {
	logger *log.Logger

	mu          sync.Mutex
	lastUsage   float64
	lastUsageAt time.Time
	haveUsage   bool
}

func newCPUCollector(logger *log.Logger) *cpuCollector {
	return &cpuCollector{logger: logger}
}

// usagePercent returns the busy fraction, sampling at most once per
// cpuUsageMinInterval so rapid successive collections agree.
func (c *cpuCollector) usagePercent(ctx context.Context) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.haveUsage && time.Since(c.lastUsageAt) < cpuUsageMinInterval {
		return c.lastUsage, nil
	}
	percentages, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return 0, err
	}
	usage := 0.0
	if len(percentages) > 0 {
		usage = percentages[0]
	}
	c.lastUsage = usage
	c.lastUsageAt = time.Now()
	c.haveUsage = true
	return usage, nil
}

func (c *cpuCollector) Collect(ctx context.Context) (CPUMetric, error) {
	c.logger.Debug("Collecting CPU usage percentage")
	usage, err := c.usagePercent(ctx)
	if err != nil {
		c.logger.Error("Failed to collect CPU usage percentage", "error", err)
		return CPUMetric{}, err
	}

	cores := runtime.NumCPU()

	c.logger.Debug("Collecting CPU load averages")
	loadStat, err := load.AvgWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect CPU load averages", "error", err)
		return CPUMetric{}, err
	}

	c.logger.Debug("Collecting CPU temperature")
	temperature := 0.0
	temperatures, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect CPU temperature with context, trying SensorsTemperatures", "error", err)
		temperatures, err = sensors.SensorsTemperatures()
	}

	if err != nil {
		c.logger.Warn("Failed to collect CPU temperature, using 0.0", "error", err)
	} else {
		c.logger.Debug("Found temperature sensors", "count", len(temperatures))

		cpuSensorKeys := []string{
			"coretemp", "k10temp", "k8temp", "cpu_thermal", "acpitz", "thermal_zone0",
			"cpu", "core", "processor", "cpu0", "cpu1", "cpu2", "cpu3",
			"cpu_thermal", "cpu_core", "cpu_die", "cpu_proximity",
			"TC0P", "TC0D", "TC0H", "TG0P", "TG0D", "TG0H", "TH0P",
			"TM0P", "TM0S", "TN0P", "TN0D", "TN0H", "TI0P", "TI1P",
			"TA0P", "TA1P", "TW0P",
			"TB0T", "TB1T", "TB2T", "TB3T",
			"thermal", "temp", "temperature",
		}

		for _, temp := range temperatures {
			sensorKeyLower := strings.ToLower(temp.SensorKey)
			for _, key := range cpuSensorKeys {
				if strings.Contains(sensorKeyLower, strings.ToLower(key)) {
					temperature = temp.Temperature
					c.logger.Debug("Found CPU temperature sensor", "key", temp.SensorKey, "temperature", temperature)
					break
				}
			}
			if temperature > 0 {
				break
			}
		}

		if temperature == 0.0 {
			for _, temp := range temperatures {
				sensorKeyLower := strings.ToLower(temp.SensorKey)
				if strings.Contains(sensorKeyLower, "thermal") ||
					strings.Contains(sensorKeyLower, "temp") ||
					strings.Contains(sensorKeyLower, "cpu") ||
					strings.Contains(sensorKeyLower, "core") {
					temperature = temp.Temperature
					c.logger.Debug("Using thermal sensor as fallback", "key", temp.SensorKey, "temperature", temperature)
					break
				}
			}
		}

		if temperature == 0.0 {
			for _, temp := range temperatures {
				if (temp.SensorKey == "TB0T" || temp.SensorKey == "TB1T") && temp.Temperature > 0 {
					temperature = temp.Temperature
					c.logger.Debug("Using battery sensor as system temperature indicator", "key", temp.SensorKey, "temperature", temperature)
					break
				}
			}
		}

		if temperature == 0.0 && len(temperatures) > 0 {
			firstTemp := temperatures[0].Temperature
			if firstTemp > 0 && firstTemp < 150 {
				temperature = firstTemp
				c.logger.Debug("Using first available temperature sensor", "key", temperatures[0].SensorKey, "temperature", temperature)
			}
		}
	}

	infoStats, err := cpu.InfoWithContext(ctx)
	if err != nil {
		c.logger.Warn("Failed to collect CPU info", "error", err)
	}
	var vendorID, family, model, modelName, microcode string
	var mhz float64
	var cacheSize int32
	var flags []string
	if len(infoStats) > 0 {
		cpu0 := infoStats[0]
		vendorID = cpu0.VendorID
		family = cpu0.Family
		model = cpu0.Model
		modelName = cpu0.ModelName
		mhz = cpu0.Mhz
		cacheSize = cpu0.CacheSize
		flags = cpu0.Flags
		microcode = cpu0.Microcode
	}

	timesStats, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		c.logger.Warn("Failed to collect CPU times", "error", err)
	}
	var user, systemTime, idle, nice, iowait, irq, softirq, steal, guest, guestNice float64
	if len(timesStats) > 0 {
		agg := timesStats[0]
		user = agg.User
		systemTime = agg.System
		idle = agg.Idle
		nice = agg.Nice
		iowait = agg.Iowait
		irq = agg.Irq
		softirq = agg.Softirq
		steal = agg.Steal
		guest = agg.Guest
		guestNice = agg.GuestNice
	}

	c.logger.Debug("CPU metrics collected successfully", "usage_percent", usage, "cores", cores, "temperature", temperature)
	return CPUMetric{
		UsagePercent: usage,
		Cores:        cores,
		LoadAvg1:     loadStat.Load1,
		LoadAvg5:     loadStat.Load5,
		LoadAvg15:    loadStat.Load15,
		Temperature:  temperature,
		VendorID:     vendorID,
		Family:       family,
		Model:        model,
		ModelName:    modelName,
		Mhz:          mhz,
		CacheSize:    cacheSize,
		Flags:        flags,
		Microcode:    microcode,
		User:         user,
		System:       systemTime,
		Idle:         idle,
		Nice:         nice,
		Iowait:       iowait,
		Irq:          irq,
		Softirq:      softirq,
		Steal:        steal,
		Guest:        guest,
		GuestNice:    guestNice,
	}, nil
}
