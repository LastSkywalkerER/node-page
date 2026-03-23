package diskmetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/disk/infrastructure/entities"
	diskrepos "system-stats/internal/modules/disk/infrastructure/repositories"
)

type mockDiskRepository struct {
	saveErr           error
	latestMetric      entities.DiskMetric
	latestErr         error
	historicalMetrics []entities.HistoricalDiskMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockDiskRepository) SaveCurrentMetric(_ context.Context, _ entities.DiskMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}
func (m *mockDiskRepository) GetLatestMetric(_ context.Context) (entities.DiskMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockDiskRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*entities.DiskMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockDiskRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]entities.HistoricalDiskMetric, error) {
	return m.historicalMetrics, m.historicalErr
}
func (m *mockDiskRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]entities.HistoricalDiskMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ diskrepos.DiskRepository = (*mockDiskRepository)(nil)

func newTestService(repo diskrepos.DiskRepository) *service {
	return &service{logger: log.Default(), diskRepository: repo}
}

func TestGetLatest_Success(t *testing.T) {
	want := entities.DiskMetric{UsagePercent: 55.0, Total: 1000}
	svc := newTestService(&mockDiskRepository{latestMetric: want})
	got, err := svc.GetLatest(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UsagePercent != want.UsagePercent {
		t.Errorf("UsagePercent = %v, want %v", got.UsagePercent, want.UsagePercent)
	}
}

func TestGetLatest_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newTestService(&mockDiskRepository{latestErr: repoErr})
	_, err := svc.GetLatest(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestGetHistorical_Success(t *testing.T) {
	want := []entities.HistoricalDiskMetric{{UsagePercent: 40}, {UsagePercent: 50}}
	svc := newTestService(&mockDiskRepository{historicalMetrics: want})
	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestGetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("query error")
	svc := newTestService(&mockDiskRepository{historicalErr: repoErr})
	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestSave_CallsRepository(t *testing.T) {
	mock := &mockDiskRepository{}
	svc := newTestService(mock)
	if err := svc.Save(context.Background(), entities.DiskMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}
