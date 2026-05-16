package httputil

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	DefaultHoursWindow = 0.0833
	// MaxHoursWindow caps ?hours to one day. Stops UI overload (Recharts) and DB DoS via huge values.
	MaxHoursWindow = 24.0
)

func ParseHoursQuery(c *gin.Context) float64 {
	hoursStr := c.DefaultQuery("hours", "0.0833")
	hours, err := strconv.ParseFloat(hoursStr, 64)
	if err != nil || hours <= 0 {
		return DefaultHoursWindow
	}
	if hours > MaxHoursWindow {
		return MaxHoursWindow
	}
	return hours
}

func ParseHostIdQuery(c *gin.Context) uint {
	hostIdStr := c.DefaultQuery("host_id", "0")
	hostId, err := strconv.ParseUint(hostIdStr, 10, 32)
	if err != nil {
		return 0
	}
	return uint(hostId)
}
