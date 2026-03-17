package database

import "gorm.io/gorm"

// TimeOffsetQuery adds a time-range filter to the query, compatible with both SQLite and PostgreSQL.
func TimeOffsetQuery(db *gorm.DB, hours float64) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres":
		return db.Where("timestamp >= NOW() - (? * INTERVAL '1 hour')", hours)
	default: // sqlite
		return db.Where("timestamp >= datetime('now', '-' || CAST(? AS TEXT) || ' hours')", hours)
	}
}

// TimeOffsetQueryWithHost adds a host-scoped time-range filter compatible with SQLite and PostgreSQL.
func TimeOffsetQueryWithHost(db *gorm.DB, hostId uint, hours float64) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres":
		return db.Where("host_id = ? AND timestamp >= NOW() - (? * INTERVAL '1 hour')", hostId, hours)
	default: // sqlite
		return db.Where("host_id = ? AND timestamp >= datetime('now', '-' || CAST(? AS TEXT) || ' hours')", hostId, hours)
	}
}
