package database

import (
	"fmt"

	"gorm.io/gorm"

	invitations "system-stats/internal/auth/invitations"
	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	nodes "system-stats/internal/cluster/nodes"
	raftcluster "system-stats/internal/cluster/raft"
	raftbridge "system-stats/internal/cluster/raft/bridge"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
)

// Migrate performs automatic schema migration for all database entities.
func Migrate(db *gorm.DB) error {
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

	err = db.AutoMigrate(&users.User{})
	if err != nil {
		return fmt.Errorf("failed to migrate users: %w", err)
	}

	err = db.AutoMigrate(&users.RefreshToken{})
	if err != nil {
		return fmt.Errorf("failed to migrate refresh tokens: %w", err)
	}

	// Fix existing invitations with NULL email before adding NOT NULL constraint
	_ = db.Exec("UPDATE user_invitations SET email = '' WHERE email IS NULL")

	err = db.AutoMigrate(&invitations.UserInvitation{})
	if err != nil {
		return fmt.Errorf("failed to migrate user invitations: %w", err)
	}

	err = db.AutoMigrate(&nodes.NodeJoinToken{}, &nodes.NodeCredential{})
	if err != nil {
		return fmt.Errorf("failed to migrate node entities: %w", err)
	}

	if err := raftcluster.AutoMigrate(db); err != nil {
		return fmt.Errorf("failed to migrate raft cluster tables: %w", err)
	}

	if err := raftbridge.AutoMigrate(db); err != nil {
		return fmt.Errorf("failed to migrate raft bridge tables: %w", err)
	}

	return nil
}
