package health

import (
	"fmt"
	"time"
)

// formatSessionUptime renders uptime rounded to whole seconds: "45s", "14m51s", "3h58m", "4d2h".
// Used for agent session length and for local process uptime in /health.
func formatSessionUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	sec := int64(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	m := sec / 60
	s := sec % 60
	if m < 60 {
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := m / 60
	m = m % 60
	days := h / 24
	h = h % 24
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, h)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
