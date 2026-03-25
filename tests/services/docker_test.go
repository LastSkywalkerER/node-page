package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	dockerservice "system-stats/internal/modules/docker/application"
	dockerrepos "system-stats/internal/modules/docker/domain/repositories"
	dockerentities "system-stats/internal/modules/docker/infrastructure/entities"
)

type mockDockerRepository struct {
	saveErr           error
	latestMetric      dockerentities.DockerMetric
	latestErr         error
	historicalMetrics []dockerrepos.HistoricalDockerMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockDockerRepository) SaveCurrentMetric(_ context.Context, _ dockerentities.DockerMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}

func (m *mockDockerRepository) GetLatestMetric(_ context.Context) (dockerentities.DockerMetric, error) {
	return m.latestMetric, m.latestErr
}

func (m *mockDockerRepository) GetLatestMetricByHost(_ context.Context, _ uint) (*dockerentities.DockerMetric, error) {
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	cp := m.latestMetric
	return &cp, nil
}

func (m *mockDockerRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]dockerrepos.HistoricalDockerMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

func (m *mockDockerRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]dockerrepos.HistoricalDockerMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

type mockDockerCollector struct {
	metric dockerentities.DockerMetric
	err    error
}

func (m *mockDockerCollector) CollectDockerMetrics(_ context.Context) (dockerentities.DockerMetric, error) {
	return m.metric, m.err
}
func (m *mockDockerCollector) IsDockerAvailable(_ context.Context) bool { return m.err == nil }
func (m *mockDockerCollector) Close() error                            { return nil }

var _ dockerrepos.DockerRepository = (*mockDockerRepository)(nil)
var _ dockerrepos.DockerMetricsCollector = (*mockDockerCollector)(nil)

func newDockerService(repo dockerrepos.DockerRepository, collector dockerrepos.DockerMetricsCollector) dockerservice.Service {
	return dockerservice.NewService(log.Default(), collector, repo)
}

func TestDocker_GetLatest_Success(t *testing.T) {
	want := dockerentities.DockerMetric{TotalContainers: 3, RunningContainers: 2}
	svc := newDockerService(&mockDockerRepository{latestMetric: want}, &mockDockerCollector{})

	got, err := svc.GetLatest(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TotalContainers != want.TotalContainers {
		t.Errorf("TotalContainers = %d, want %d", got.TotalContainers, want.TotalContainers)
	}
}

func TestDocker_GetLatest_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newDockerService(&mockDockerRepository{latestErr: repoErr}, &mockDockerCollector{})

	_, err := svc.GetLatest(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestDocker_GetHistorical_Success(t *testing.T) {
	want := []dockerrepos.HistoricalDockerMetric{{TotalContainers: 5}, {TotalContainers: 6}}
	svc := newDockerService(&mockDockerRepository{historicalMetrics: want}, &mockDockerCollector{})

	got, err := svc.GetHistorical(context.Background(), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("len = %d, want %d", len(got), len(want))
	}
}

func TestDocker_GetHistorical_RepoError(t *testing.T) {
	repoErr := errors.New("query error")
	svc := newDockerService(&mockDockerRepository{historicalErr: repoErr}, &mockDockerCollector{})

	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestDocker_Save_CallsRepository(t *testing.T) {
	mock := &mockDockerRepository{}
	svc := newDockerService(mock, &mockDockerCollector{})

	if err := svc.Save(context.Background(), dockerentities.DockerMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestDocker_CollectAndSave_CollectorError(t *testing.T) {
	collectErr := errors.New("docker unavailable")
	svc := newDockerService(&mockDockerRepository{}, &mockDockerCollector{err: collectErr})

	err := svc.CollectAndSave(context.Background(), 1)
	if !errors.Is(err, collectErr) {
		t.Errorf("expected collector error, got %v", err)
	}
}
