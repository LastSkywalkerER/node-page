// Package metricshost resolves which host ID metric APIs should use and validates the host exists.
package metricshost

import (
	"context"
	"errors"

	"gorm.io/gorm"

	hostservice "system-stats/internal/modules/hosts/application"
)

// ErrHostNotFound is returned when the query references a host_id that does not exist in the database.
var ErrHostNotFound = errors.New("host not found")

// EffectiveHostID maps the optional host_id query parameter to a concrete DB host row.
// Zero means "this server instance" (current host by MAC upsert flow).
func EffectiveHostID(ctx context.Context, hosts hostservice.Service, queryHostID uint) (uint, error) {
	if queryHostID > 0 {
		_, err := hosts.GetHostByID(ctx, queryHostID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, ErrHostNotFound
			}
			return 0, err
		}
		return queryHostID, nil
	}
	h, err := hosts.GetCurrentHost(ctx)
	if err != nil {
		return 0, err
	}
	return h.ID, nil
}

// IsRemoteHost reports whether effectiveHostID refers to a machine other than this process.
// Used for sensors (always local) and SSE (only local collector produces live events).
func IsRemoteHost(ctx context.Context, hosts hostservice.Service, effectiveHostID uint) (bool, error) {
	current, err := hosts.GetCurrentHost(ctx)
	if err != nil {
		return false, err
	}
	return effectiveHostID != current.ID, nil
}
