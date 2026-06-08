package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	memory "system-stats/internal/metrics/memory"
)

type mockMemoryRepository struct {
	saveErr           error
	latestMetric      memory.MemoryMetric
	latestErr         error
	historicalMetrics []memory.HistoricalMemoryMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockMemoryRepository) SaveCurrentMetric(_ context.Context, _ memory.MemoryMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockMemoryRepository) SaveCurrentMetricAt(_ context.Context, _ memory.MemoryMetric, _ uint, _ time.Time) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockMemoryRepository) GetLatestMetric(_ context.Context) (memory.MemoryMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockMemoryRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*memory.MemoryMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockMemoryRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]memory.HistoricalMemoryMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

func (m *mockMemoryRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]memory.HistoricalMemoryMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ memory.Repository = (*mockMemoryRepository)(nil)

func newMemoryService(repo memory.Repository) memory.Service {
	return memory.NewService(log.Default(), repo)
}

func TestMemory_GetHistorical_Success(t *testing.T) {
	want := []memory.HistoricalMemoryMetric{{UsagePercent: 70}, {UsagePercent: 80}}
	svc := newMemoryService(&mockMemoryRepository{historicalMetrics: want})

	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestMemory_GetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newMemoryService(&mockMemoryRepository{historicalErr: repoErr})

	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestMemory_GetHistoricalByHost_Success(t *testing.T) {
	want := []memory.HistoricalMemoryMetric{{UsagePercent: 55}}
	svc := newMemoryService(&mockMemoryRepository{historicalMetrics: want})

	got, err := svc.GetHistoricalByHost(context.Background(), 1, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}

func TestMemory_Save_CallsRepository(t *testing.T) {
	mock := &mockMemoryRepository{}
	svc := newMemoryService(mock)

	if err := svc.Save(context.Background(), memory.MemoryMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestMemory_Save_RepoError(t *testing.T) {
	repoErr := errors.New("save error")
	svc := newMemoryService(&mockMemoryRepository{saveErr: repoErr})

	err := svc.Save(context.Background(), memory.MemoryMetric{}, 1)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}
