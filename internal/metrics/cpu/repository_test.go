package cpu

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newCPUTestRepo(t *testing.T) Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?_pragma=foreign_keys(on)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&HistoricalCPUMetric{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRepository(db)
}

// The CPU model is static per machine but must be persisted on the metric row
// so the /hosts response can surface it at read time (the node card no longer
// waits on a live SSE tick for the model). Guard the save/read round-trip.
func TestCPUModelNameRoundTrips(t *testing.T) {
	repo := newCPUTestRepo(t)
	ctx := context.Background()

	if err := repo.SaveCurrentMetric(ctx, CPUMetric{
		UsagePercent: 12.5,
		Cores:        8,
		ModelName:    "Intel(R) Core(TM) i5-9400 CPU @ 2.90GHz",
	}, 7); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.GetLatestMetricByHost(ctx, 7)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got == nil {
		t.Fatal("latest metric is nil")
	}
	if got.ModelName != "Intel(R) Core(TM) i5-9400 CPU @ 2.90GHz" {
		t.Errorf("ModelName = %q, want the saved model", got.ModelName)
	}
	if got.Cores != 8 {
		t.Errorf("Cores = %d, want 8", got.Cores)
	}
}
