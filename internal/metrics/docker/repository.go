package docker

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"system-stats/internal/app/dbutil"
)

type dockerRepository struct {
	db *gorm.DB

	// sigMu guards lastSig: host_id -> container id -> content hash (excluding the
	// per-tick timestamp). It lets SaveCurrentMetricAt skip re-UPSERTing rows that
	// did not change since the last persist — idle containers (identical stats)
	// stop re-sending their full, static-heavy rows (labels/ports/image), which is
	// the dominant app->DB write volume.
	sigMu   sync.Mutex
	lastSig map[uint]map[string]uint64
}

// NewRepository creates a new Docker repository.
func NewRepository(db *gorm.DB) DockerRepository {
	return &dockerRepository{db: db, lastSig: map[uint]map[string]uint64{}}
}

// entitySig hashes a container row excluding its per-tick MetricTimestamp, so an
// otherwise-unchanged (idle) container hashes the same tick-to-tick.
func entitySig(e DockerContainerEntity) uint64 {
	e.MetricTimestamp = time.Time{}
	b, err := json.Marshal(e)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

func (r *dockerRepository) SaveCurrentMetric(ctx context.Context, metric DockerMetric, hostId uint) error {
	return r.SaveCurrentMetricAt(ctx, metric, hostId, time.Now().UTC())
}

func (r *dockerRepository) SaveCurrentMetricAt(ctx context.Context, metric DockerMetric, hostId uint, ts time.Time) error {
	// Save as historical metric
	timestamp := ts.UTC()
	historicalMetric := HistoricalDockerMetric{
		HostID:            &hostId,
		Timestamp:         timestamp,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}

	// Convert containers from all stacks to current-state entities.
	var containerEntities []DockerContainerEntity
	currentIDs := make([]string, 0, 16)
	for _, stack := range metric.Stacks {
		for _, container := range stack.Containers {
			entity, err := container.ToDockerContainerEntity(hostId, timestamp)
			if err != nil {
				return err
			}
			containerEntities = append(containerEntities, entity)
			currentIDs = append(currentIDs, entity.ID)
		}
	}

	// Content-hash gate: only UPSERT containers whose row changed since the last
	// persist (excluding the per-tick timestamp). Idle containers are skipped, so
	// their static-heavy rows (labels/ports/image) are not re-sent every persist —
	// the bulk of the app->DB write volume. Always keep currentIDs for the prune.
	r.sigMu.Lock()
	hostSigs := r.lastSig[hostId]
	r.sigMu.Unlock()
	changed := make([]DockerContainerEntity, 0, len(containerEntities))
	newSigs := make(map[string]uint64, len(containerEntities))
	for _, e := range containerEntities {
		sig := entitySig(e)
		newSigs[e.ID] = sig
		if prev, ok := hostSigs[e.ID]; !ok || prev != sig {
			changed = append(changed, e)
		}
	}

	// Save metric and containers in transaction
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The counter row is a per-tick time series (PK host_id, timestamp) kept
		// for the historical container-count chart. Raft-log replay re-applies
		// entries after a restart, so the same (host_id, timestamp) row must be a
		// no-op, not a pkey violation.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&historicalMetric).Error; err != nil {
			return err
		}
		// Container rows are CURRENT STATE: one row per (host_id, id), upserted in
		// place. This keeps the table bounded to the live container count instead
		// of appending N rows every tick forever. The conflict target is the full
		// composite PK so a container id shared across hosts (cloned VMs) does not
		// clobber another host's row. Only CHANGED rows are written (see the gate).
		if len(changed) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}, {Name: "host_id"}},
				UpdateAll: true,
			}).Create(&changed).Error; err != nil {
				return err
			}
		}
		// Prune containers that no longer exist on this host. Only when Docker is
		// actually available do we have an authoritative current set — a transient
		// daemon outage must not wipe the last-known list. With Docker available
		// and zero containers, the NOT-IN guard is skipped so the host is cleared.
		if metric.DockerAvailable {
			del := tx.Where("host_id = ?", hostId)
			if len(currentIDs) > 0 {
				del = del.Where("id NOT IN ?", currentIDs)
			}
			if err := del.Delete(&DockerContainerEntity{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Don't advance the signature cache on failure — the unwritten rows stay
		// "changed" and get retried on the next persist.
		return err
	}

	// Commit signatures only after a successful write; drop gone containers so a
	// future re-add is re-written.
	r.sigMu.Lock()
	r.lastSig[hostId] = newSigs
	r.sigMu.Unlock()
	return nil
}

// containersForHost loads the current container set for a host and groups it by
// stack, the shape buildMetricFromHistorical expects.
func (r *dockerRepository) containersForHost(ctx context.Context, hostId uint) (map[string][]DockerContainer, error) {
	var entities []DockerContainerEntity
	if err := r.db.WithContext(ctx).Where("host_id = ?", hostId).Find(&entities).Error; err != nil {
		return nil, err
	}
	containerMap := make(map[string][]DockerContainer)
	for _, entity := range entities {
		container, err := entity.ToDockerContainer()
		if err != nil {
			return nil, err
		}
		stackName := ExtractStackNameFromContainerName(container.Name)
		containerMap[stackName] = append(containerMap[stackName], container)
	}
	return containerMap, nil
}

func (r *dockerRepository) GetLatestMetric(ctx context.Context) (DockerMetric, error) {
	var metric HistoricalDockerMetric
	err := r.db.WithContext(ctx).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		return DockerMetric{}, err
	}

	containerMap := make(map[string][]DockerContainer)
	if metric.HostID != nil {
		containerMap, err = r.containersForHost(ctx, *metric.HostID)
		if err != nil {
			return DockerMetric{}, err
		}
	}

	result, err := buildMetricFromHistorical(metric, containerMap)
	if err != nil {
		return DockerMetric{}, err
	}
	return result, nil
}

func (r *dockerRepository) GetLatestMetricByHost(ctx context.Context, hostId uint) (*DockerMetric, error) {
	var metric HistoricalDockerMetric
	err := r.db.WithContext(ctx).
		Where("host_id = ?", hostId).
		Order("timestamp DESC").
		First(&metric).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	containerMap, err := r.containersForHost(ctx, hostId)
	if err != nil {
		return nil, err
	}

	result, err := buildMetricFromHistorical(metric, containerMap)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func buildMetricFromHistorical(metric HistoricalDockerMetric, containerMap map[string][]DockerContainer) (DockerMetric, error) {
	var stacks []DockerStack
	for stackName, containers := range containerMap {
		sort.Slice(containers, func(i, j int) bool {
			return containers[i].Name < containers[j].Name
		})

		runningContainers := 0
		for _, container := range containers {
			if container.State == "running" {
				runningContainers++
			}
		}

		stacks = append(stacks, DockerStack{
			Name:              stackName,
			Containers:        containers,
			TotalContainers:   len(containers),
			RunningContainers: runningContainers,
		})
	}

	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].Name < stacks[j].Name
	})

	return DockerMetric{
		Stacks:            stacks,
		TotalContainers:   metric.TotalContainers,
		RunningContainers: metric.RunningContainers,
		DockerAvailable:   metric.DockerAvailable,
	}, nil
}

func (r *dockerRepository) GetHistoricalMetrics(ctx context.Context, hours float64) ([]HistoricalDockerMetric, error) {
	var metrics []HistoricalDockerMetric
	err := dbutil.TimeOffsetQuery(r.db.WithContext(ctx), hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}

func (r *dockerRepository) GetHistoricalMetricsByHost(ctx context.Context, hostId uint, hours float64) ([]HistoricalDockerMetric, error) {
	var metrics []HistoricalDockerMetric
	err := dbutil.TimeOffsetQueryWithHost(r.db.WithContext(ctx), hostId, hours).
		Order("timestamp ASC").
		Find(&metrics).Error
	return metrics, err
}
