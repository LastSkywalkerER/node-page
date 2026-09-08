// Package hwsensors is the ONE place temperature sensors are read. The cpu
// collector (CPU temperature) and the sensors endpoint used to walk the
// sensor tree independently — and the cpu collector retried the identical
// call on a warning — so one metrics tick could scan hwmon two or three
// times. Every hwmon attribute open is a real bus transaction on the driver
// side (SMBus / IPMI / SMC), so the walk is bounded here: results are cached
// for a few seconds and shared, the read runs behind a timeout so a slow or
// wedged driver degrades ONE metric instead of stalling the whole tick, and
// on Linux the sensor file set is discovered once and only the live
// temp*_input files are re-read (see hwsensors_linux.go).
package hwsensors

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Stat mirrors gopsutil's TemperatureStat so callers can switch without
// changing their JSON. SensorKey follows gopsutil's "<chip>[_<label>]" form.
type Stat struct {
	SensorKey   string
	Temperature float64
	High        float64
	Critical    float64
}

const (
	// cacheTTL is comfortably shorter than the metrics tick, so every tick
	// reads fresh values while the cpu collector and the sensors service
	// (and an on-demand GET /sensors) inside one tick share one read.
	cacheTTL = 5 * time.Second
	// readTimeout bounds one sensor read. The first read gets a longer
	// budget: discovery walks the whole tree and some drivers are slow to
	// answer the first time.
	readTimeout      = 3 * time.Second
	firstReadTimeout = 10 * time.Second
)

// ErrTimeout is returned when the sensor read exceeded its budget and no
// earlier result exists to serve instead.
var ErrTimeout = errors.New("hwsensors: read timed out")

// backend is the platform reader (hwmon on Linux, gopsutil elsewhere).
type backend interface {
	read(ctx context.Context) ([]Stat, error)
}

// Reader caches and time-bounds a backend.
type Reader struct {
	b backend

	mu       sync.Mutex
	cached   []Stat
	cachedAt time.Time
	ever     bool // a read completed at least once
	inflight bool // a read is still running past its deadline
}

var def = &Reader{b: newBackend()}

// Temperatures returns the current sensor readings through the shared
// process-wide reader. The returned slice must be treated as read-only.
func Temperatures(ctx context.Context) ([]Stat, error) { return def.Temperatures(ctx) }

// Temperatures serves the cached readings while fresh, otherwise runs one
// read behind a timeout. A read that overruns keeps running in the
// background and refreshes the cache when it lands; meanwhile the previous
// readings (if any) are served rather than blocking the caller.
func (r *Reader) Temperatures(ctx context.Context) ([]Stat, error) {
	r.mu.Lock()
	if r.ever && time.Since(r.cachedAt) < cacheTTL {
		out := r.cached
		r.mu.Unlock()
		return out, nil
	}
	if r.inflight {
		// The previous read is still stuck; don't stack another one.
		out, ever := r.cached, r.ever
		r.mu.Unlock()
		if !ever {
			return nil, ErrTimeout
		}
		return out, nil
	}
	r.inflight = true
	budget := readTimeout
	if !r.ever {
		budget = firstReadTimeout
	}
	r.mu.Unlock()

	type result struct {
		stats []Stat
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var res result
		defer func() {
			if rec := recover(); rec != nil {
				res = result{err: fmt.Errorf("hwsensors: read panicked: %v", rec)}
			}
			r.mu.Lock()
			r.inflight = false
			if res.err == nil || len(res.stats) > 0 {
				r.cached = res.stats
				r.cachedAt = time.Now()
				r.ever = true
			}
			r.mu.Unlock()
			ch <- res
		}()
		// The read is detached from the caller's ctx on purpose: a caller
		// that gives up must not cancel a read whose result the next caller
		// will want.
		res.stats, res.err = r.b.read(context.Background())
	}()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case res := <-ch:
		if res.err != nil && len(res.stats) == 0 {
			return nil, res.err
		}
		// gopsutil reports per-sensor read failures as a non-nil "warnings"
		// error next to valid data; the data is what callers want.
		return res.stats, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	r.mu.Lock()
	out, ever := r.cached, r.ever
	r.mu.Unlock()
	if !ever {
		return nil, ErrTimeout
	}
	return out, nil
}
