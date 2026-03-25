package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	diskservice "system-stats/internal/modules/disk/application"
	diskentities "system-stats/internal/modules/disk/infrastructure/entities"
	diskrepos "system-stats/internal/modules/disk/infrastructure/repositories"
)

type mockDiskRepository struct {
	saveErr           error
	latestMetric      diskentities.DiskMetric
	latestErr         error
	historicalMetrics []diskentities.HistoricalDiskMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockDiskRepository) SaveCurrentMetric(_ context.Context, _ diskentities.DiskMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockDiskRepository) GetLatestMetric(_ context.Context) (diskentities.DiskMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockDiskRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*diskentities.DiskMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockDiskRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]diskentities.HistoricalDiskMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

func (m *mockDiskRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]diskentities.HistoricalDiskMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ diskrepos.DiskRepository = (*mockDiskRepository)(nil)

func newDiskService(repo diskrepos.DiskRepository) diskservice.Service {
	return diskservice.NewService(log.Default(), repo)
}

func TestDisk_GetLatest_Success(t *testing.T) {
	want := diskentities.DiskMetric{UsagePercent: 55.0, Total: 1000}
	svc := newDiskService(&mockDiskRepository{latestMetric: want})

	got, err := svc.GetLatest(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UsagePercent != want.UsagePercent {
		t.Errorf("UsagePercent = %v, want %v", got.UsagePercent, want.UsagePercent)
	}
}

func TestDisk_GetLatest_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newDiskService(&mockDiskRepository{latestErr: repoErr})

	_, err := svc.GetLatest(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestDisk_GetHistorical_Success(t *testing.T) {
	want := []diskentities.HistoricalDiskMetric{{UsagePercent: 40}, {UsagePercent: 50}}
	svc := newDiskService(&mockDiskRepository{historicalMetrics: want})

	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestDisk_GetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("query error")
	svc := newDiskService(&mockDiskRepository{historicalErr: repoErr})

	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestDisk_Save_CallsRepository(t *testing.T) {
	mock := &mockDiskRepository{}
	svc := newDiskService(mock)

	if err := svc.Save(context.Background(), diskentities.DiskMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}
