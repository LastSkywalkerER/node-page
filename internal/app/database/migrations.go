/**
 * Package database provides database migration functionality.
 * This file contains migration logic for all database entities.
 */
package database

import (
	"fmt"

	"gorm.io/gorm"

	cpuentities "system-stats/internal/modules/cpu/infrastructure/entities"
	diskentities "system-stats/internal/modules/disk/infrastructure/entities"
	dockerdomain "system-stats/internal/modules/docker/domain/repositories"
	hostentities "system-stats/internal/modules/hosts/infrastructure/entities"
	memoryentities "system-stats/internal/modules/memory/infrastructure/entities"
	networkentities "system-stats/internal/modules/network/infrastructure/entities"
	userentities "system-stats/internal/modules/users/infrastructure/entities"
)

/**
 * Migrate performs automatic schema migration for all database entities.
 * This function creates all necessary tables and ensures proper foreign key relationships.
 *
 * @param db The GORM database instance
 * @return error Returns an error if migration fails
 */
func Migrate(db *gorm.DB) error {
	// Auto-migrate all historical metric entities to create database tables
	err := db.AutoMigrate(
		&cpuentities.HistoricalCPUMetric{},
		&memoryentities.HistoricalMemoryMetric{},
		&diskentities.HistoricalDiskMetric{},
		&networkentities.HistoricalNetworkMetric{},
		&dockerdomain.HistoricalDockerMetric{},
		&hostentities.Host{},
	)
	if err != nil {
		return fmt.Errorf("failed to migrate historical metrics: %w", err)
	}

	// Migrate user entities separately to ensure proper foreign key relationships
	err = db.AutoMigrate(&userentities.User{})
	if err != nil {
		return fmt.Errorf("failed to migrate users: %w", err)
	}

	err = db.AutoMigrate(&userentities.RefreshToken{})
	if err != nil {
		return fmt.Errorf("failed to migrate refresh tokens: %w", err)
	}

	return nil
}
