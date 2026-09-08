//go:build !linux

package hwsensors

import (
	"context"

	"github.com/shirou/gopsutil/v4/sensors"
)

// gopsutilBackend is the non-Linux reader (IOKit / SMC on macOS, WMI on
// Windows). Its per-call cost is what it is; the shared cache above keeps it
// to one call per tick.
type gopsutilBackend struct{}

func newBackend() backend { return gopsutilBackend{} }

func (gopsutilBackend) read(ctx context.Context) ([]Stat, error) {
	temps, err := sensors.TemperaturesWithContext(ctx)
	out := make([]Stat, 0, len(temps))
	for _, t := range temps {
		out = append(out, Stat{SensorKey: t.SensorKey, Temperature: t.Temperature, High: t.High, Critical: t.Critical})
	}
	return out, err
}
