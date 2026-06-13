// Package runtimetune holds small runtime/GC hygiene helpers shared by the
// server and the controller sidecar.
package runtimetune

import (
	"context"
	"runtime/debug"
	"time"
)

// StartPeriodicRelease launches a background goroutine that periodically returns
// freed heap memory to the OS via debug.FreeOSMemory.
//
// Go's scavenger releases freed pages with MADV_FREE, which leaves them mapped
// (still counted in RSS) until the kernel reclaims them under memory pressure —
// so a long-running process's RSS drifts UP toward the GOMEMLIMIT high-water
// mark and never visibly comes back down, even though the live heap is small.
// FreeOSMemory forces a GC and a MADV_DONTNEED scavenge, so RSS tracks the live
// heap. It is cheap on a small heap (sub-millisecond) and runs on a slow cadence,
// so the steady-state CPU cost is negligible.
//
// The goroutine exits when ctx is done (pass the app's shutdown context; the
// controller, which has no graceful-shutdown context, may pass context.Background
// — it then runs for the process lifetime, which is fine for a sidecar).
func StartPeriodicRelease(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				debug.FreeOSMemory()
			}
		}
	}()
}
