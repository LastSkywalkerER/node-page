package dockermetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/log"

	"system-stats/internal/modules/docker/domain/repositories"
	"system-stats/internal/modules/docker/infrastructure/entities"
)

// mockDockerRepository implements repositories.DockerRepository.
type mockDockerRepository struct {
	saveErr           error
	latestMetric      entities.DockerMetric
	latestErr         error
	historicalMetrics []repositories.HistoricalDockerMetric
	historicalErr     error
	saveCalled        bool
}

func (m *mockDockerRepository) SaveCurrentMetric(_ context.Context, _ entities.DockerMetric, _ uint) error {
	m.saveCalled = true
	return m.saveErr
}
func (m *mockDockerRepository) GetLatestMetric(_ context.Context) (entities.DockerMetric, error) {
	return m.latestMetric, m.latestErr
}
func (m *mockDockerRepository) GetHistoricalMetrics(_ context.Context, _ float64) ([]repositories.HistoricalDockerMetric, error) {
	return m.historicalMetrics, m.historicalErr
}
func (m *mockDockerRepository) GetHistoricalMetricsByHost(_ context.Context, _ uint, _ float64) ([]repositories.HistoricalDockerMetric, error) {
	return m.historicalMetrics, m.historicalErr
}

// mockDockerCollector implements repositories.DockerMetricsCollector.
type mockDockerCollector struct {
	metric entities.DockerMetric
	err    error
}

func (m *mockDockerCollector) CollectDockerMetrics(_ context.Context) (entities.DockerMetric, error) {
	return m.metric, m.err
}
func (m *mockDockerCollector) IsDockerAvailable(_ context.Context) bool { return m.err == nil }

var _ repositories.DockerRepository = (*mockDockerRepository)(nil)
var _ repositories.DockerMetricsCollector = (*mockDockerCollector)(nil)

func newTestService(repo repositories.DockerRepository, collector repositories.DockerMetricsCollector) *service {
	return &service{
		logger:           log.Default(),
		collector:        collector,
		dockerRepository: repo,
	}
}

func TestGetLatest_Success(t *testing.T) {
	want := entities.DockerMetric{TotalContainers: 3, RunningContainers: 2}
	svc := newTestService(&mockDockerRepository{latestMetric: want}, &mockDockerCollector{})
	got, err := svc.GetLatest(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TotalContainers != want.TotalContainers {
		t.Errorf("TotalContainers = %d, want %d", got.TotalContainers, want.TotalContainers)
	}
}

func TestGetLatest_RepoError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newTestService(&mockDockerRepository{latestErr: repoErr}, &mockDockerCollector{})
	_, err := svc.GetLatest(context.Background())
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestGetHistorical_Success(t *testing.T) {
	want := []repositories.HistoricalDockerMetric{{TotalContainers: 5}, {TotalContainers: 6}}
	svc := newTestService(&mockDockerRepository{historicalMetrics: want}, &mockDockerCollector{})
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
	svc := newTestService(&mockDockerRepository{historicalErr: repoErr}, &mockDockerCollector{})
	_, err := svc.GetHistorical(context.Background(), 1.0)
	if !errors.Is(err, repoErr) {
		t.Errorf("expected repo error, got %v", err)
	}
}

func TestSave_CallsRepository(t *testing.T) {
	mock := &mockDockerRepository{}
	svc := newTestService(mock, &mockDockerCollector{})
	if err := svc.Save(context.Background(), entities.DockerMetric{}, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.saveCalled {
		t.Error("expected SaveCurrentMetric to be called")
	}
}

func TestCollectAndSave_CollectorError(t *testing.T) {
	collectErr := errors.New("docker unavailable")
	svc := newTestService(&mockDockerRepository{}, &mockDockerCollector{err: collectErr})
	err := svc.CollectAndSave(context.Background(), 1)
	if !errors.Is(err, collectErr) {
		t.Errorf("expected collector error, got %v", err)
	}
}
