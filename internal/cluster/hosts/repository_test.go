package hosts_test

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
)

// TestDeleteHostCascade_RealSchema migrates the actual entities (so the cascade's
// hardcoded table names must match GORM's real names) and verifies a host + its
// docker rows are removed without the "commit unexpectedly resulted in rollback"
// that a wrong table name (docker_containers vs docker_container_entities) caused.
func TestDeleteHostCascade_RealSchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&cpu.HistoricalCPUMetric{},
		&memory.HistoricalMemoryMetric{},
		&disk.HistoricalDiskMetric{},
		&network.HistoricalNetworkMetric{},
		&docker.HistoricalDockerMetric{},
		&docker.DockerContainerEntity{},
		&hosts.Host{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	r := hosts.NewRepository(db)
	ctx := context.Background()
	two := uint(2)
	ts := time.Now().UTC().Truncate(time.Second)

	if err := db.Create(&hosts.Host{ID: 2, Name: "peer", MacAddress: "aa:bb:cc:dd:ee:ff"}).Error; err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := db.Create(&docker.HistoricalDockerMetric{HostID: &two, Timestamp: ts, TotalContainers: 1}).Error; err != nil {
		t.Fatalf("seed docker metric: %v", err)
	}
	if err := db.Create(&docker.DockerContainerEntity{ID: "c1", MetricTimestamp: ts, Name: "peer-web"}).Error; err != nil {
		t.Fatalf("seed docker container: %v", err)
	}

	if err := r.DeleteHostCascade(ctx, 2); err != nil {
		t.Fatalf("DeleteHostCascade returned error (the bug): %v", err)
	}

	var hostCount, dockerCount, ctrCount int64
	db.Model(&hosts.Host{}).Where("id = ?", 2).Count(&hostCount)
	db.Model(&docker.HistoricalDockerMetric{}).Where("host_id = ?", 2).Count(&dockerCount)
	db.Model(&docker.DockerContainerEntity{}).Where("id = ?", "c1").Count(&ctrCount)
	if hostCount != 0 || dockerCount != 0 || ctrCount != 0 {
		t.Fatalf("rows remain after cascade: host=%d docker=%d container=%d", hostCount, dockerCount, ctrCount)
	}
}
