package rammetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/memory/infrastructure/entities"
	memrepos "system-stats/internal/modules/memory/infrastructure/repositories"
)

type mockMemoryRepository struct {
	saveErr           error
	latestMetric      entities.MemoryMetric
	latestErr         error
	historicalMetrics []entities.HistoricalMemoryMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockMemoryRepository) SaveCurrentMetric(_ context.Context, _ entities.MemoryMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}
func (m *mockMemoryRepository) GetLatestMetric(_ context.Context) (entities.MemoryMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockMemoryRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*entities.MemoryMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockMemoryRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]entities.HistoricalMemoryMetric, error) {
	return m.historicalMetrics, m.historicalErr
}
func (m *mockMemoryRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]entities.HistoricalMemoryMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ memrepos.MemoryRepository = (*mockMemoryRepository)(nil)

func newTestService(repo memrepos.MemoryRepository) *service {
	return &service{logger: log.Default(), memoryRepository: repo}
}

func TestGetHistorical_Success(t *testing.T) {
	want := []entities.HistoricalMemoryMetric{{UsagePercent: 70}, {UsagePercent: 80}}
	svc := newTestService(&mockMemoryRepository{historicalMetrics: want})
	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestGetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newTestService(&mockMemoryRepository{historicalErr: repoErr})
	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestGetHistoricalByHost_Success(t *testing.T) {
	want := []entities.HistoricalMemoryMetric{{UsagePercent: 55}}
	svc := newTestService(&mockMemoryRepository{historicalMetrics: want})
	got, err := svc.GetHistoricalByHost(context.Background(), 1, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}

func TestSave_CallsRepository(t *testing.T) {
	mock := &mockMemoryRepository{}
	svc := newTestService(mock)
	if err := svc.Save(context.Background(), entities.MemoryMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestSave_RepoError(t *testing.T) {
	repoErr := errors.New("save error")
	svc := newTestService(&mockMemoryRepository{saveErr: repoErr})
	err := svc.Save(context.Background(), entities.MemoryMetric{}, 1)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}
