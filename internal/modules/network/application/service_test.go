package networkmetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/network/infrastructure/entities"
	networkrepos "system-stats/internal/modules/network/infrastructure/repositories"
)

type mockNetworkRepository struct {
	saveErr           error
	latestMetric      entities.NetworkMetric
	latestErr         error
	historicalMetrics []entities.NetworkMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockNetworkRepository) SaveCurrentMetric(_ context.Context, _ entities.NetworkMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}
func (m *mockNetworkRepository) GetLatestMetric(_ context.Context) (entities.NetworkMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockNetworkRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*entities.NetworkMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockNetworkRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]entities.NetworkMetric, error) {
	return m.historicalMetrics, m.historicalErr
}
func (m *mockNetworkRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]entities.NetworkMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ networkrepos.NetworkRepository = (*mockNetworkRepository)(nil)

func newTestService(repo networkrepos.NetworkRepository) *service {
	return &service{logger: log.Default(), networkRepository: repo}
}

func TestGetHistorical_Success(t *testing.T) {
	want := []entities.NetworkMetric{{}, {}}
	svc := newTestService(&mockNetworkRepository{historicalMetrics: want})
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
	svc := newTestService(&mockNetworkRepository{historicalErr: repoErr})
	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestGetHistoricalByHost_Success(t *testing.T) {
	want := []entities.NetworkMetric{{}}
	svc := newTestService(&mockNetworkRepository{historicalMetrics: want})
	got, err := svc.GetHistoricalByHost(context.Background(), 1, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}

func TestSave_CallsRepository(t *testing.T) {
	mock := &mockNetworkRepository{}
	svc := newTestService(mock)
	if err := svc.Save(context.Background(), entities.NetworkMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestSave_RepoError(t *testing.T) {
	repoErr := errors.New("save error")
	svc := newTestService(&mockNetworkRepository{saveErr: repoErr})
	err := svc.Save(context.Background(), entities.NetworkMetric{}, 1)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}
