package raft

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	hosts "system-stats/internal/cluster/hosts"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
)

// MetricSink persists ONE replicated host's metric batch into this node's local
// state (the metric history tables + the docker container-inventory current
// state) and pushes it to the SSE broker — WITHOUT going through the durable
// Raft consensus log.
//
// It is the shared core of two callers:
//   - the best-effort intra/inter-cluster metric stream (the live path), and
//   - the legacy CmdMetricBatch FSM applier (kept only for wire-compat / replay).
//
// Metrics are high-volume, low-value, drop-tolerant time-series; routing them
// through fsync'd consensus is what ballooned the raft log. The sink writes them
// directly instead. Host SPECS/identity still flow through Raft (CmdHostUpsert),
// so the sink only ATTACHES metrics to an already-known host row; a host not yet
// replicated here is a transient no-op (its upsert lands separately).
type MetricSink struct {
	logger       *log.Logger
	hostRepo     hosts.Repository
	cpu          cpu.Repository
	memory       memory.Repository
	disk         disk.Repository
	network      network.Repository
	docker       docker.DockerRepository
	publish      func(data []byte)
	localCluster string
}

// NewMetricSink builds a sink from the same dependency bundle the FSM appliers
// use, so DI wires the repositories + broker exactly once.
func NewMetricSink(deps AppliersDeps) *MetricSink {
	return &MetricSink{
		logger:       deps.Logger,
		hostRepo:     deps.HostRepo,
		cpu:          deps.CPURepo,
		memory:       deps.MemoryRepo,
		disk:         deps.DiskRepo,
		network:      deps.NetworkRepo,
		docker:       deps.DockerRepo,
		publish:      deps.Publish,
		localCluster: deps.ClusterID,
	}
}

// Ingest writes the batch for one host. origin is the SENDER's cluster id; pass
// "" or the local cluster id for a same-cluster (intra-cluster) batch. Returns
// nil (not an error) when the host is unknown here yet — best-effort: the next
// cycle re-sends the full current state once the host's upsert has replicated.
func (s *MetricSink) Ingest(ctx context.Context, p MetricBatchPayload, origin string) error {
	if origin == s.localCluster {
		origin = ""
	}

	host, err := s.hostRepo.GetHostByMacAddress(ctx, p.HostMAC)
	if err == nil && host.ID == hosts.LocalCollectorHostID && origin != "" {
		// A remote machine whose docker-bridge MAC collides with our own local
		// row — the MAC isn't its identity here; resolve by (origin, name) below.
		host, err = nil, gorm.ErrRecordNotFound
	}
	if errors.Is(err, gorm.ErrRecordNotFound) && origin != "" {
		host, err = s.hostRepo.GetHostByOriginAndName(ctx, origin, p.HostName)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // host not replicated here yet — transient, self-heals
		}
		return err
	}
	if host.ID == hosts.LocalCollectorHostID {
		return nil // our own machine — the local collector already saved these
	}
	ts := p.Timestamp

	save := func(module string, fn func() error) {
		if err := fn(); err != nil && s.logger != nil {
			s.logger.Warn("metric sink: save", "module", module, "host_id", host.ID, "error", err)
		}
	}
	if len(p.CPU) > 0 {
		var m cpu.CPUMetric
		if json.Unmarshal(p.CPU, &m) == nil {
			save("cpu", func() error { return s.cpu.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Memory) > 0 {
		var m memory.MemoryMetric
		if json.Unmarshal(p.Memory, &m) == nil {
			save("memory", func() error { return s.memory.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Disk) > 0 {
		var m disk.DiskMetric
		if json.Unmarshal(p.Disk, &m) == nil {
			save("disk", func() error { return s.disk.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Network) > 0 {
		var m network.NetworkMetric
		if json.Unmarshal(p.Network, &m) == nil {
			save("network", func() error { return s.network.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Docker) > 0 {
		var m docker.DockerMetric
		if json.Unmarshal(p.Docker, &m) == nil {
			// The docker save writes BOTH the time-series row AND upserts the
			// container-inventory current-state (prune-on-write), so the app list
			// for this remote host self-heals on every received batch.
			save("docker", func() error { return s.docker.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}

	// A host actively streaming metrics is alive: bump its liveness so replicated
	// hosts go green without a separate heartbeat (nothing else maintains
	// last_seen for them on receiving nodes).
	if err := s.hostRepo.UpdateLastSeenAndAgentSession(ctx, host.ID, ts, nil); err != nil && s.logger != nil {
		s.logger.Warn("metric sink: bump last_seen", "host_id", host.ID, "error", err)
	}

	// Push the same snapshot to this node's SSE broker so browsers viewing this
	// (remote) host on this node update live — uniform SSE for every host.
	if s.publish != nil {
		env := map[string]any{"collecting_host_id": host.ID, "timestamp": ts}
		if len(p.CPU) > 0 {
			env["cpu"] = p.CPU
		}
		if len(p.Memory) > 0 {
			env["memory"] = p.Memory
		}
		if len(p.Disk) > 0 {
			env["disk"] = p.Disk
		}
		if len(p.Network) > 0 {
			env["network"] = p.Network
		}
		if len(p.Docker) > 0 {
			env["docker"] = p.Docker
		}
		if b, err := json.Marshal(env); err == nil {
			s.publish(b)
		}
	}
	return nil
}
