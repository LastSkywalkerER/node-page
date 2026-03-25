package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	netservice "system-stats/internal/modules/network/application"
	netentities "system-stats/internal/modules/network/infrastructure/entities"
	netrepos "system-stats/internal/modules/network/infrastructure/repositories"
)

type mockNetworkRepository struct {
	saveErr           error
	latestMetric      netentities.NetworkMetric
	latestErr         error
	historicalMetrics []netentities.NetworkMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockNetworkRepository) SaveCurrentMetric(_ context.Context, _ netentities.NetworkMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockNetworkRepository) GetLatestMetric(_ context.Context) (netentities.NetworkMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockNetworkRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*netentities.NetworkMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockNetworkRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]netentities.NetworkMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

func (m *mockNetworkRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]netentities.NetworkMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

var _ netrepos.NetworkRepository = (*mockNetworkRepository)(nil)

func newNetworkService(repo netrepos.NetworkRepository) netservice.Service {
	return netservice.NewService(log.Default(), repo)
}

func TestNetwork_GetHistorical_Success(t *testing.T) {
	want := []netentities.NetworkMetric{{}, {}}
	svc := newNetworkService(&mockNetworkRepository{historicalMetrics: want})

	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestNetwork_GetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newNetworkService(&mockNetworkRepository{historicalErr: repoErr})

	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestNetwork_GetHistoricalByHost_Success(t *testing.T) {
	want := []netentities.NetworkMetric{{}}
	svc := newNetworkService(&mockNetworkRepository{historicalMetrics: want})

	got, err := svc.GetHistoricalByHost(context.Background(), 1, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1", len(got))
	}
}

func TestNetwork_Save_CallsRepository(t *testing.T) {
	mock := &mockNetworkRepository{}
	svc := newNetworkService(mock)

	if err := svc.Save(context.Background(), netentities.NetworkMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestNetwork_Save_RepoError(t *testing.T) {
	repoErr := errors.New("save error")
	svc := newNetworkService(&mockNetworkRepository{saveErr: repoErr})

	err := svc.Save(context.Background(), netentities.NetworkMetric{}, 1)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}
