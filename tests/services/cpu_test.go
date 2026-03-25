package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	cpuservice "system-stats/internal/modules/cpu/application"
	cpuentities "system-stats/internal/modules/cpu/infrastructure/entities"
	cpurepos "system-stats/internal/modules/cpu/infrastructure/repositories"
)

type mockCPURepository struct {
	saveErr           error
	latestMetric      cpuentities.CPUMetric
	latestErr         error
	historicalMetrics []cpuentities.HistoricalCPUMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockCPURepository) SaveCurrentMetric(_ context.Context, _ cpuentities.CPUMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockCPURepository) GetLatestMetric(_ context.Context) (cpuentities.CPUMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockCPURepository) GetLatestMetricByHost(_ context.Context, _ uint) (*cpuentities.CPUMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockCPURepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]cpuentities.HistoricalCPUMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

func (m *mockCPURepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]cpuentities.HistoricalCPUMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ cpurepos.CPURepository = (*mockCPURepository)(nil)

func newCPUService(repo cpurepos.CPURepository) cpuservice.Service {
	return cpuservice.NewService(log.Default(), repo)
}

func TestCPU_GetLatest_Success(t *testing.T) {
	want := cpuentities.CPUMetric{UsagePercent: 42.5, Cores: 4}
	svc := newCPUService(&mockCPURepository{latestMetric: want})

	got, err := svc.GetLatest(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UsagePercent != want.UsagePercent {
		t.Errorf("UsagePercent = %v, want %v", got.UsagePercent, want.UsagePercent)
	}
}

func TestCPU_GetLatest_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newCPUService(&mockCPURepository{latestErr: repoErr})

	_, err := svc.GetLatest(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestCPU_Save_Success(t *testing.T) {
	mock := &mockCPURepository{}
	svc := newCPUService(mock)

	err := svc.Save(context.Background(), cpuentities.CPUMetric{UsagePercent: 10}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestCPU_Save_RepoError(t *testing.T) {
	repoErr := errors.New("save failed")
	svc := newCPUService(&mockCPURepository{saveErr: repoErr})

	err := svc.Save(context.Background(), cpuentities.CPUMetric{}, 1)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestCPU_GetHistorical_Success(t *testing.T) {
	want := []cpuentities.HistoricalCPUMetric{{Usage: 55.0}, {Usage: 60.0}}
	svc := newCPUService(&mockCPURepository{historicalMetrics: want})

	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestCPU_GetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("query error")
	svc := newCPUService(&mockCPURepository{historicalErr: repoErr})

	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestCPU_GetHistoricalByHost_Success(t *testing.T) {
	want := []cpuentities.HistoricalCPUMetric{{Usage: 30.0}}
	svc := newCPUService(&mockCPURepository{historicalMetrics: want})

	got, err := svc.GetHistoricalByHost(context.Background(), 2, 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}
