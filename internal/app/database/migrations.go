package database

import (
	"fmt"

	"gorm.io/gorm"

	invitations "system-stats/internal/auth/invitations"
	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	raftcluster "system-stats/internal/cluster/raft"
	raftbridge "system-stats/internal/cluster/raft/bridge"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	connectors "system-stats/internal/platform/connectors"
)

// Migrate performs automatic schema migration for all database entities.
func Migrate(db *gorm.DB) error {
	// Drop the legacy docker_container_entities table (append-per-tick time
	// series) BEFORE AutoMigrate so it is recreated in the current-state shape
	// (one upserted row per container, with a host_id column). Idempotent no-op
	// once migrated. See dropLegacyDockerContainerEntities.
	if err := dropLegacyDockerContainerEntities(db); err != nil {
		return fmt.Errorf("failed to drop legacy docker_container_entities: %w", err)
	}

	err := db.AutoMigrate(
		&cpu.HistoricalCPUMetric{},
		&memory.HistoricalMemoryMetric{},
		&disk.HistoricalDiskMetric{},
		&network.HistoricalNetworkMetric{},
		&docker.HistoricalDockerMetric{},
		&docker.DockerContainerEntity{},
		&hosts.Host{},
	)
	if err != nil {
		return fmt.Errorf("failed to migrate historical metrics: %w", err)
	}

	// Upgrade the metric tables from a timestamp-only PK to a composite
	// (host_id, timestamp) PK and drop the unusable docker_metrics FK. Runs
	// AFTER AutoMigrate (so columns/tables exist) and is idempotent on both
	// SQLite and Postgres. AutoMigrate itself never changes an existing PK, so
	// this hand-written step performs the structural rebuild on upgraded DBs;
	// fresh DBs already get the composite PK from AutoMigrate and this is a no-op.
	if err := migrateMetricCompositePKs(db); err != nil {
		return fmt.Errorf("failed to migrate metric composite primary keys: %w", err)
	}

	// Downgrade the legacy UNIQUE idx_hosts_name to a plain (non-unique) index.
	// hosts.name is a display label, not an identity key — connector guests and
	// cross-cluster-bridged hosts can transiently share a name with an agent
	// host until merged by mac_address/external_id, and the unique index turned
	// that into a Postgres "duplicate key" hot-loop on the periodic name UPDATE.
	// AutoMigrate never downgrades an existing unique index on its own, so this
	// explicit step must run after the hosts AutoMigrate to fix upgraded DBs.
	if err := relaxHostsNameUniqueIndex(db); err != nil {
		return fmt.Errorf("failed to relax hosts.name unique index: %w", err)
	}

	err = db.AutoMigrate(&users.User{})
	if err != nil {
		return fmt.Errorf("failed to migrate users: %w", err)
	}

	err = db.AutoMigrate(&users.RefreshToken{})
	if err != nil {
		return fmt.Errorf("failed to migrate refresh tokens: %w", err)
	}

	err = db.AutoMigrate(&connectors.Connector{})
	if err != nil {
		return fmt.Errorf("failed to migrate connectors: %w", err)
	}

	// Fix existing invitations with NULL email before adding NOT NULL constraint
	_ = db.Exec("UPDATE user_invitations SET email = '' WHERE email IS NULL")

	err = db.AutoMigrate(&invitations.UserInvitation{})
	if err != nil {
		return fmt.Errorf("failed to migrate user invitations: %w", err)
	}

	if err := raftcluster.AutoMigrate(db); err != nil {
		return fmt.Errorf("failed to migrate raft cluster tables: %w", err)
	}

	if err := raftbridge.AutoMigrate(db); err != nil {
		return fmt.Errorf("failed to migrate raft bridge tables: %w", err)
	}

	return nil
}
