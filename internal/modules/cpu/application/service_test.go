package cpumetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/cpu/infrastructure/entities"
	cpurepos "system-stats/internal/modules/cpu/infrastructure/repositories"
)

// mockCPURepository implements cpurepos.CPURepository for testing.
type mockCPURepository struct {
	saveErr            error
	latestMetric       entities.CPUMetric
	latestErr          error
	historicalMetrics  []entities.HistoricalCPUMetric
	historicalErr      error
	saveCalled         bool
}

func (m *mockCPURepository) SaveCurrentMetric(_ context.Context, _ entities.CPUMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockCPURepository) GetLatestMetric(_ context.Context) (entities.CPUMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockCPURepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]entities.HistoricalCPUMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

func (m *mockCPURepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]entities.HistoricalCPUMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ cpurepos.CPURepository = (*mockCPURepository)(nil)

func newTestService(repo cpurepos.CPURepository) *service {
	return &service{
		logger:        log.Default(),
		cpuRepository: repo,
	}
}

func TestGetLatest_Success(t *testing.T) {
	want := entities.CPUMetric{UsagePercent: 42.5, Cores: 4}
	svc := newTestService(&mockCPURepository{latestMetric: want})

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
	svc := newTestService(&mockCPURepository{latestErr: repoErr})

	_, err := svc.GetLatest(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestSave_Success(t *testing.T) {
	mock := &mockCPURepository{}
	svc := newTestService(mock)

	err := svc.Save(context.Background(), entities.CPUMetric{UsagePercent: 10}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestSave_RepoError(t *testing.T) {
	repoErr := errors.New("save failed")
	svc := newTestService(&mockCPURepository{saveErr: repoErr})

	err := svc.Save(context.Background(), entities.CPUMetric{}, 1)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestGetHistorical_Success(t *testing.T) {
	want := []entities.HistoricalCPUMetric{{Usage: 55.0}, {Usage: 60.0}}
	svc := newTestService(&mockCPURepository{historicalMetrics: want})

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
	svc := newTestService(&mockCPURepository{historicalErr: repoErr})

	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestGetHistoricalByHost_Success(t *testing.T) {
	want := []entities.HistoricalCPUMetric{{Usage: 30.0}}
	svc := newTestService(&mockCPURepository{historicalMetrics: want})

	got, err := svc.GetHistoricalByHost(context.Background(), 2, 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}
