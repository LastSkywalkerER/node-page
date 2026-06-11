package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	hraft "github.com/hashicorp/raft"
	"gorm.io/gorm"

	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	docker "system-stats/internal/metrics/docker"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
	connectors "system-stats/internal/platform/connectors"
)

// AppliersDeps bundles every repository the FSM needs to dispatch into.
// DI builds this once when Raft is enabled and calls RegisterAppliers.
type AppliersDeps struct {
	Logger *log.Logger
	DB     *gorm.DB
	// ClusterID is the LOCAL cluster id; commands whose OriginClusterID
	// differs stamp it onto host rows so the UI can badge uplinked sites.
	ClusterID        string
	HostRepo         hosts.Repository
	UserRepo         users.UserRepository
	RefreshTokenRepo users.RefreshTokenRepository
	CPURepo          cpu.Repository
	MemoryRepo       memory.Repository
	DiskRepo         disk.Repository
	NetworkRepo      network.Repository
	DockerRepo       docker.DockerRepository
	ConnectorRepo    connectors.Repository
	// Publish pushes a live SSE envelope (JSON) to this node's stream broker.
	// Wired so a replicated peer's metrics also stream live to browsers viewing
	// that peer on this node — uniform SSE for every host. Nil disables it.
	Publish func(data []byte)
}

// RegisterAppliers wires every CommandType this commit knows about to a
// concrete CommandApplier on the FSM. Subsequent commits will add more
// types here (metrics, peer-advertise, bridge-ack, …).
//
// All appliers run on a single goroutine inside FSM.Apply, so they do not
// need to coordinate with each other. They MUST use only cmd.Timestamp for
// any wall-clock value to keep every replica's SQLite byte-equal.
func RegisterAppliers(fsm *FSM, deps AppliersDeps) {
	if fsm == nil {
		return
	}
	a := &appliers{deps: deps}

	fsm.Register(CmdHostUpsert, a.applyHostUpsert)
	fsm.Register(CmdHostDelete, a.applyHostDelete)
	fsm.Register(CmdHostLastSeen, a.applyHostLastSeen)
	fsm.Register(CmdConnectorHostUpsert, a.applyConnectorHostUpsert)

	fsm.Register(CmdConnectorUpsert, a.applyConnectorUpsert)
	fsm.Register(CmdConnectorDelete, a.applyConnectorDelete)

	fsm.Register(CmdMetricBatch, a.applyMetricBatch)

	fsm.Register(CmdUserUpsert, a.applyUserUpsert)
	fsm.Register(CmdUserDelete, a.applyUserDelete)
	fsm.Register(CmdRefreshTokenIssue, a.applyRefreshTokenIssue)
	fsm.Register(CmdRefreshTokenRevoke, a.applyRefreshTokenRevoke)

	fsm.Register(CmdAuthSecretSet, a.applyAuthSecretSet)
	fsm.Register(CmdConfigSet, a.applyConfigSet)
	fsm.Register(CmdPeerNodeAdvertise, a.applyPeerNodeAdvertise)
	fsm.Register(CmdPeerNodeRemove, a.applyPeerNodeRemove)

	fsm.Register(CmdJoinTokenIssue, a.applyJoinTokenIssue)
	fsm.Register(CmdJoinTokenConsume, a.applyJoinTokenConsume)

	fsm.Register(CmdBridgeAck, a.applyBridgeAck)
}

type appliers struct {
	deps AppliersDeps
}

// applierCtx returns a context flagged as an applier so any future
// WriteGate plumbing recognises the call site as legitimate, plus a short
// deadline so a wedged DB cannot stall the Raft log forever.
func (a *appliers) applierCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(WithApplier(context.Background()), 10*time.Second)
}

// remoteOrigin returns the foreign cluster id, or "" for local commands.
func (a *appliers) remoteOrigin(cmd Command) string {
	if cmd.OriginClusterID != "" && cmd.OriginClusterID != a.deps.ClusterID {
		return cmd.OriginClusterID
	}
	return ""
}

func (a *appliers) applyHostUpsert(cmd Command, _ *hraft.Log) error {
	var p HostUpsertPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	_, err := a.deps.HostRepo.UpsertHost(ctx, hosts.HostInfo{
		OriginCluster:        a.remoteOrigin(cmd),
		Name:                 p.Name,
		MacAddress:           p.MacAddress,
		IPv4:                 p.IPv4,
		OS:                   p.OS,
		Platform:             p.Platform,
		PlatformFamily:       p.PlatformFamily,
		PlatformVersion:      p.PlatformVersion,
		KernelVersion:        p.KernelVersion,
		VirtualizationSystem: p.VirtualizationSystem,
		VirtualizationRole:   p.VirtualizationRole,
		HostID:               p.HostID,
		HardwareUUID:         p.HardwareUUID,
		BootTime:             p.BootTime,
	})
	return err
}

func (a *appliers) applyConnectorHostUpsert(cmd Command, _ *hraft.Log) error {
	var p ConnectorHostUpsertPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	_, err := a.deps.HostRepo.UpsertConnectorHost(ctx, hosts.ConnectorHostInfo{
		HostInfo: hosts.HostInfo{
			OriginCluster:        a.remoteOrigin(cmd),
			Name:                 p.Name,
			MacAddress:           p.MacAddress,
			IPv4:                 p.IPv4,
			OS:                   p.OS,
			Platform:             p.Platform,
			PlatformFamily:       p.PlatformFamily,
			PlatformVersion:      p.PlatformVersion,
			KernelVersion:        p.KernelVersion,
			VirtualizationSystem: p.VirtualizationSystem,
			VirtualizationRole:   p.VirtualizationRole,
			HostID:               p.HostID,
			HardwareUUID:         p.HardwareUUID,
			BootTime:             p.BootTime,
		},
		HostType:    p.HostType,
		ParentMAC:   p.ParentMAC,
		ExternalID:  p.ExternalID,
		GuestStatus: p.GuestStatus,
	})
	return err
}

func (a *appliers) applyConnectorUpsert(cmd Command, _ *hraft.Log) error {
	var p ConnectorUpsertPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.Fingerprint == "" {
		return errors.New("raft: ConnectorUpsert requires fingerprint")
	}
	if a.deps.ConnectorRepo == nil {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.ConnectorRepo.Upsert(ctx, &connectors.Connector{
		Type:          p.Type,
		Endpoint:      p.Endpoint,
		TokenID:       p.TokenID,
		SecretEnc:     p.SecretEnc,
		SkipTLSVerify: p.SkipTLSVerify,
		Fingerprint:   p.Fingerprint,
		Config:        p.Config,
		Enabled:       p.Enabled,
	})
}

func (a *appliers) applyConnectorDelete(cmd Command, _ *hraft.Log) error {
	var p ConnectorDeletePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.Fingerprint == "" {
		return errors.New("raft: ConnectorDelete requires fingerprint")
	}
	if a.deps.ConnectorRepo == nil {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	if err := a.deps.ConnectorRepo.DeleteByFingerprint(ctx, p.Fingerprint); err != nil {
		return err
	}
	return a.deps.HostRepo.UnlinkConnectorHosts(ctx,
		connectors.ExternalIDPrefix(p.Type, p.Fingerprint), p.RemoveHosts)
}

func (a *appliers) applyHostDelete(cmd Command, _ *hraft.Log) error {
	var p HostDeletePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.HostMAC == "" {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	host, err := a.deps.HostRepo.GetHostByMacAddress(ctx, p.HostMAC)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Already gone here (or never replicated) — nothing to do.
			return nil
		}
		return err
	}
	if host.ID == hosts.LocalCollectorHostID {
		// This MAC is THIS node's own collector row. A live node must not delete
		// its own data via a replicated command; it keeps re-publishing itself
		// anyway. (Removing a live cluster member is done via Raft leave/kick.)
		return nil
	}
	return a.deps.HostRepo.DeleteHostCascade(ctx, host.ID)
}

func (a *appliers) applyHostLastSeen(cmd Command, _ *hraft.Log) error {
	var p HostLastSeenPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	if err := a.deps.HostRepo.UpdateLastSeenAndAgentSession(ctx, p.HostID, p.LastSeen, p.AgentSessionStartedAt); err != nil {
		return err
	}
	if p.Name != "" || p.IPv4 != "" {
		if err := a.deps.HostRepo.UpdateHostLabelsFromAgentPush(ctx, p.HostID, p.Name, p.IPv4); err != nil {
			return err
		}
	}
	return nil
}

// applyMetricBatch persists a replicated host's metrics. The host is resolved
// by MAC to the LOCAL row id (which differs per node). On the origin node that
// row is the local collector (id=1), whose metrics the collector already wrote
// directly — so we skip it to avoid duplicates; every other node stores the
// batch under its own id for that host. Per-module saves are best-effort: a
// decode or write hiccup on one metric is logged, never fatal to consensus.
func (a *appliers) applyMetricBatch(cmd Command, _ *hraft.Log) error {
	var p MetricBatchPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()

	host, err := a.deps.HostRepo.GetHostByMacAddress(ctx, p.HostMAC)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Host not replicated here yet — its CmdHostUpsert lands earlier in
			// log order, so this is only a transient first-cycle race.
			return nil
		}
		return err
	}
	if host.ID == hosts.LocalCollectorHostID {
		// Our own machine — the local collector already saved these directly.
		return nil
	}
	ts := p.Timestamp

	save := func(module string, raw json.RawMessage, fn func() error) {
		if len(raw) == 0 {
			return
		}
		if err := fn(); err != nil && a.deps.Logger != nil {
			a.deps.Logger.Warn("raft: apply metric batch", "module", module, "host_id", host.ID, "error", err)
		}
	}

	if len(p.CPU) > 0 {
		var m cpu.CPUMetric
		if json.Unmarshal(p.CPU, &m) == nil {
			save("cpu", p.CPU, func() error { return a.deps.CPURepo.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Memory) > 0 {
		var m memory.MemoryMetric
		if json.Unmarshal(p.Memory, &m) == nil {
			save("memory", p.Memory, func() error { return a.deps.MemoryRepo.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Disk) > 0 {
		var m disk.DiskMetric
		if json.Unmarshal(p.Disk, &m) == nil {
			save("disk", p.Disk, func() error { return a.deps.DiskRepo.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Network) > 0 {
		var m network.NetworkMetric
		if json.Unmarshal(p.Network, &m) == nil {
			save("network", p.Network, func() error { return a.deps.NetworkRepo.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}
	if len(p.Docker) > 0 {
		var m docker.DockerMetric
		if json.Unmarshal(p.Docker, &m) == nil {
			save("docker", p.Docker, func() error { return a.deps.DockerRepo.SaveCurrentMetricAt(ctx, m, host.ID, ts) })
		}
	}

	// Push the same snapshot to this node's SSE broker so browsers viewing this
	// (remote) host on this node update live — uniform SSE for every host.
	if a.deps.Publish != nil {
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
			a.deps.Publish(b)
		}
	}
	return nil
}

func (a *appliers) applyUserUpsert(cmd Command, _ *hraft.Log) error {
	var p UserUpsertPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()

	// Look up by email to keep IDs deterministic across replicas: the
	// SQLite ROWID assigned to the first Create wins on every node since
	// they all apply commands in the same log order.
	existing, err := a.deps.UserRepo.FindByEmail(ctx, p.Email)
	if err != nil {
		return err
	}
	if existing == nil {
		u := &users.User{
			Email:        p.Email,
			PasswordHash: p.PasswordHash,
			Role:         p.Role,
			CreatedAt:    cmd.Timestamp,
			UpdatedAt:    cmd.Timestamp,
		}
		if p.ID != 0 {
			u.ID = p.ID
		}
		if err := a.deps.UserRepo.Create(ctx, u); err != nil {
			return err
		}
		return nil
	}
	// Existing — only role updates flow through this path post-creation;
	// password changes are intentionally not part of this commit's surface.
	if p.Role != "" && existing.Role != p.Role {
		return a.deps.UserRepo.UpdateRole(ctx, existing.ID, p.Role)
	}
	return nil
}

func (a *appliers) applyUserDelete(cmd Command, _ *hraft.Log) error {
	var p UserDeletePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.UserRepo.Delete(ctx, p.UserID)
}

func (a *appliers) applyRefreshTokenIssue(cmd Command, _ *hraft.Log) error {
	var p RefreshTokenIssuePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = cmd.Timestamp
	}
	return a.deps.RefreshTokenRepo.Create(ctx, &users.RefreshToken{
		UserID:    p.UserID,
		JTI:       p.JTI,
		TokenHash: p.TokenHash,
		ExpiresAt: p.ExpiresAt,
		CreatedAt: createdAt,
	})
}

func (a *appliers) applyRefreshTokenRevoke(cmd Command, _ *hraft.Log) error {
	var p RefreshTokenRevokePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	if p.JTI != "" {
		return a.deps.RefreshTokenRepo.RevokeByJTI(ctx, p.JTI)
	}
	if p.UserID != 0 {
		return a.deps.RefreshTokenRepo.RevokeAllByUserID(ctx, p.UserID)
	}
	return errors.New("raft: RefreshTokenRevoke needs jti or user_id")
}

func (a *appliers) applyAuthSecretSet(cmd Command, _ *hraft.Log) error {
	var p AuthSecretSetPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	if err := upsertClusterConfig(ctx, a.deps.DB, "jwt_secret", p.JWTSecret, cmd.Timestamp); err != nil {
		return err
	}
	return upsertClusterConfig(ctx, a.deps.DB, "refresh_secret", p.RefreshSecret, cmd.Timestamp)
}

func (a *appliers) applyConfigSet(cmd Command, _ *hraft.Log) error {
	var p ConfigSetPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return upsertClusterConfig(ctx, a.deps.DB, p.Key, p.Value, cmd.Timestamp)
}

func (a *appliers) applyPeerNodeAdvertise(cmd Command, _ *hraft.Log) error {
	var p PeerNodeAdvertisePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.ClusterID == "" || p.NodeID == "" {
		return errors.New("raft: PeerNodeAdvertise requires cluster_id and node_id")
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	row := peerNodeAdvertise{
		ClusterID:    p.ClusterID,
		NodeID:       p.NodeID,
		URL:          p.URL,
		Capabilities: joinCSV(p.Capabilities),
		UpdatedAt:    cmd.Timestamp,
	}
	// Upsert keyed by (cluster_id, node_id).
	res := a.deps.DB.WithContext(ctx).
		Where("cluster_id = ? AND node_id = ?", p.ClusterID, p.NodeID).
		Assign(map[string]any{
			"url":          row.URL,
			"capabilities": row.Capabilities,
			"updated_at":   row.UpdatedAt,
		}).
		FirstOrCreate(&row)
	if res.Error != nil {
		return res.Error
	}
	// A URL identifies one node endpoint: rows advertising the same URL
	// under another identity are leftovers from a cluster/node rename and
	// would haunt the bridge picker (it filters by cluster id, which the
	// stale row no longer matches) — drop them.
	if p.URL != "" {
		a.deps.DB.WithContext(ctx).
			Where("url = ? AND NOT (cluster_id = ? AND node_id = ?)", p.URL, p.ClusterID, p.NodeID).
			Delete(&peerNodeAdvertise{})
	}
	return nil
}

func (a *appliers) applyPeerNodeRemove(cmd Command, _ *hraft.Log) error {
	var p PeerNodeRemovePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.DB.WithContext(ctx).
		Where("cluster_id = ? AND node_id = ?", p.ClusterID, p.NodeID).
		Delete(&peerNodeAdvertise{}).Error
}

func (a *appliers) applyJoinTokenIssue(cmd Command, _ *hraft.Log) error {
	var p JoinTokenIssuePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.TokenHash == "" {
		return errors.New("raft: JoinTokenIssue requires token_hash")
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	row := clusterJoinToken{
		TokenHash: p.TokenHash,
		ExpiresAt: p.ExpiresAt,
		CreatedBy: p.CreatedBy,
		CreatedAt: cmd.Timestamp,
	}
	return a.deps.DB.WithContext(ctx).Create(&row).Error
}

func (a *appliers) applyJoinTokenConsume(cmd Command, _ *hraft.Log) error {
	var p JoinTokenConsumePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.TokenHash == "" {
		return errors.New("raft: JoinTokenConsume requires token_hash")
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	consumed := cmd.Timestamp
	return a.deps.DB.WithContext(ctx).
		Model(&clusterJoinToken{}).
		Where("token_hash = ? AND consumed_at IS NULL", p.TokenHash).
		Updates(map[string]any{
			"consumed_at":  &consumed,
			"by_node_id":   p.ByNodeID,
			"by_node_addr": p.ByNodeAddr,
		}).Error
}

func (a *appliers) applyBridgeAck(cmd Command, _ *hraft.Log) error {
	var p BridgeAckPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.PeerClusterID == "" {
		return errors.New("raft: BridgeAck requires peer_cluster_id")
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return upsertClusterConfig(ctx, a.deps.DB,
		fmt.Sprintf("bridge_ack:%s", p.PeerClusterID),
		fmt.Sprintf("%d", p.LastOriginIndex),
		cmd.Timestamp,
	)
}
