package database

import (
	"gorm.io/gorm"

	docker "system-stats/internal/metrics/docker"
)

// dropLegacyDockerContainerEntities upgrades docker_container_entities from the
// legacy append-per-tick time series (PK id, metric_timestamp; no host_id) to
// the current-state table (PK id, with a host_id column). The rows are
// ephemeral — fully rebuilt on the next collection tick — so the cleanest
// upgrade is to drop the legacy table and let AutoMigrate recreate it in the new
// shape. It MUST run BEFORE the AutoMigrate that creates the table, and is
// idempotent: it only drops when the legacy shape (a table without a host_id
// column) is present, so on a fresh DB or an already-migrated DB it is a no-op.
//
// Dropping here also neutralises the docker_container_entities handling in
// migrateMetricCompositePKs: the recreated table carries no foreign key, so the
// FK-drop there finds nothing to do.
func dropLegacyDockerContainerEntities(db *gorm.DB) error {
	const table = "docker_container_entities"
	if !db.Migrator().HasTable(table) {
		return nil
	}
	// HasColumn inspects the live table for the column backing the struct field.
	// Present == already migrated to the current-state shape.
	if db.Migrator().HasColumn(&docker.DockerContainerEntity{}, "host_id") {
		return nil
	}
	return db.Migrator().DropTable(table)
}
