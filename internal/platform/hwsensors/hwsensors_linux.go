//go:build linux

package hwsensors

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/sensors"
)

// hwmonBackend reads /sys/class/hwmon the way node_exporter does, minus the
// per-scrape rediscovery: the file set (chip name, label, high/critical
// thresholds — all static) is discovered once and re-validated every
// rediscoverEvery, and each read touches only the live temp*_input files.
// gopsutil re-globs the tree and reads 3–5 files per sensor on every call.
//
// Output is kept byte-for-byte compatible with gopsutil's Linux reader:
// SensorKey = "<name>" or "<name>_<label>" (label lower-cased, spaces →
// underscores), values in °C, High/Critical from temp*_max / temp*_crit.
type hwmonBackend struct {
	mu           sync.Mutex
	files        []sensorFile
	discoveredAt time.Time
	noHwmon      bool
}

type sensorFile struct {
	key   string
	input string
	high  float64
	crit  float64
}

const (
	rediscoverEvery = 10 * time.Minute
	milli           = 1000.0
)

func newBackend() backend { return &hwmonBackend{} }

func hostSys() string {
	if v := strings.TrimSpace(os.Getenv("HOST_SYS")); v != "" {
		return filepath.Clean(v)
	}
	return "/sys"
}

func (b *hwmonBackend) read(ctx context.Context) ([]Stat, error) {
	b.mu.Lock()
	if b.files == nil || time.Since(b.discoveredAt) > rediscoverEvery {
		b.files = discover()
		b.discoveredAt = time.Now()
		b.noHwmon = len(b.files) == 0
	}
	files, noHwmon := b.files, b.noHwmon
	b.mu.Unlock()

	if noHwmon {
		// Boards without hwmon (Raspberry Pi and friends) expose
		// /sys/class/thermal/thermal_zone* instead; gopsutil's fallback
		// handles those (2 reads per zone — nothing to optimise).
		temps, err := sensors.TemperaturesWithContext(ctx)
		out := make([]Stat, 0, len(temps))
		for _, t := range temps {
			out = append(out, Stat{SensorKey: t.SensorKey, Temperature: t.Temperature, High: t.High, Critical: t.Critical})
		}
		return out, err
	}

	out := make([]Stat, 0, len(files))
	vanished := false
	for _, f := range files {
		raw, err := readSmall(f.input)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				vanished = true // device unplugged / module unloaded
			}
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			continue
		}
		out = append(out, Stat{SensorKey: f.key, Temperature: v / milli, High: f.high, Critical: f.crit})
	}
	if vanished {
		b.mu.Lock()
		b.files = nil // rediscover on the next read
		b.mu.Unlock()
	}
	return out, nil
}

// discover walks the hwmon tree once: temp*_input files, their chip name,
// label and static thresholds.
func discover() []sensorFile {
	sys := hostSys()
	inputs, _ := filepath.Glob(filepath.Join(sys, "class/hwmon/hwmon*/temp*_input"))
	if len(inputs) == 0 {
		// CentOS keeps the attributes one level down, under device/.
		inputs, _ = filepath.Glob(filepath.Join(sys, "class/hwmon/hwmon*/device/temp*_input"))
	}
	out := make([]sensorFile, 0, len(inputs))
	chipNames := make(map[string]string) // hwmon dir → name (read once per chip)
	for _, input := range inputs {
		dir := filepath.Dir(input)
		base := strings.Split(filepath.Base(input), "_")[0] // temp1
		basepath := filepath.Join(dir, base)

		name, ok := chipNames[dir]
		if !ok {
			raw, err := readSmall(filepath.Join(dir, "name"))
			if err != nil {
				continue
			}
			name = strings.TrimSpace(raw)
			chipNames[dir] = name
		}
		key := name
		if raw, err := readSmall(basepath + "_label"); err == nil {
			label := strings.Join(strings.Split(strings.TrimSpace(strings.ToLower(raw)), " "), "_")
			if label != "" {
				key = name + "_" + label
			}
		}
		out = append(out, sensorFile{
			key:   key,
			input: input,
			high:  optionalMilli(basepath + "_max"),
			crit:  optionalMilli(basepath + "_crit"),
		})
	}
	return out
}

func optionalMilli(path string) float64 {
	raw, err := readSmall(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return v / milli
}

// readSmall reads a sysfs attribute with one plain read(2) into a fixed
// buffer. os.ReadFile would stat first (sysfs lies about sizes) and, on
// drivers that answer EAGAIN, park the file in the netpoller and spin; a
// direct read either returns the value or fails at once.
func readSmall(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf [128]byte
	n, err := syscall.Read(int(f.Fd()), buf[:])
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}
