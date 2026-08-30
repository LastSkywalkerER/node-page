package raft

import (
	"context"
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
	gateway "system-stats/internal/platform/gateway"
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
	GatewayRepo      gateway.Repository
	GatewayBlockRepo gateway.BlockRepository
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
	a := &appliers{deps: deps, dispatch: map[CommandType]CommandApplier{}, metricSink: NewMetricSink(deps)}

	// register wires the applier into the FSM and the local dispatch map so
	// applyBridgeEnvelopeBatch can fan a wrapped batch out to the same
	// handlers without a second consensus round.
	register := func(t CommandType, fn CommandApplier) {
		a.dispatch[t] = fn
		fsm.Register(t, fn)
	}

	register(CmdHostUpsert, a.applyHostUpsert)
	register(CmdHostDelete, a.applyHostDelete)
	register(CmdHostLastSeen, a.applyHostLastSeen)
	register(CmdConnectorHostUpsert, a.applyConnectorHostUpsert)

	register(CmdConnectorUpsert, a.applyConnectorUpsert)
	register(CmdConnectorDelete, a.applyConnectorDelete)

	register(CmdGatewayRouteUpsert, a.applyGatewayRouteUpsert)
	register(CmdGatewayRouteDelete, a.applyGatewayRouteDelete)
	register(CmdGatewayBlockUpsert, a.applyGatewayBlockUpsert)
	register(CmdGatewayBlockDelete, a.applyGatewayBlockDelete)

	register(CmdMetricBatch, a.applyMetricBatch)

	register(CmdUserUpsert, a.applyUserUpsert)
	register(CmdUserPasswordChange, a.applyUserPasswordChange)
	register(CmdUserDelete, a.applyUserDelete)
	register(CmdRefreshTokenIssue, a.applyRefreshTokenIssue)
	register(CmdRefreshTokenRevoke, a.applyRefreshTokenRevoke)

	register(CmdAuthSecretSet, a.applyAuthSecretSet)
	register(CmdConfigSet, a.applyConfigSet)
	register(CmdPeerNodeAdvertise, a.applyPeerNodeAdvertise)
	register(CmdPeerNodeRemove, a.applyPeerNodeRemove)

	register(CmdJoinTokenIssue, a.applyJoinTokenIssue)
	register(CmdJoinTokenConsume, a.applyJoinTokenConsume)

	register(CmdBridgeAck, a.applyBridgeAck)
	fsm.Register(CmdBridgeEnvelopeBatch, a.applyBridgeEnvelopeBatch)
}

type appliers struct {
	deps AppliersDeps
	// dispatch mirrors the FSM registration so a wrapped bridge batch can
	// fan out to the same appliers (deliberately NOT including the batch
	// applier itself - no recursion).
	dispatch map[CommandType]CommandApplier
	// metricSink shares the metric-persist core with the best-effort metric
	// stream; the legacy CmdMetricBatch applier just delegates to it.
	metricSink *MetricSink
}

// applyBridgeEnvelopeBatch unwraps a replication batch and applies each
// inner command via the regular appliers. Per-entry failures are logged and
// skipped: one bad envelope must not fail the whole consensus entry (it has
// already been committed to the log).
func (a *appliers) applyBridgeEnvelopeBatch(cmd Command, _ *hraft.Log) error {
	var p BridgeEnvelopeBatchPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	for _, e := range p.Entries {
		fn, ok := a.dispatch[e.Type]
		if !ok {
			if a.deps.Logger != nil {
				a.deps.Logger.Warn("raft: bridge batch carries unknown command type", "type", uint16(e.Type))
			}
			continue
		}
		inner := Command{
			Type:            e.Type,
			OriginClusterID: e.OriginClusterID,
			OriginNodeID:    e.OriginNodeID,
			OriginIndex:     e.OriginIndex,
			Timestamp:       e.Timestamp,
			Payload:         e.Payload,
		}
		if err := fn(inner, nil); err != nil && a.deps.Logger != nil {
			a.deps.Logger.Warn("raft: bridge batch entry failed", "type", uint16(e.Type), "origin_index", e.OriginIndex, "error", err)
		}
	}
	return nil
}

// defaultApplierBudget is the per-apply deadline so a wedged DB cannot stall
// the Raft log forever. A few commands (host cascade delete) override it.
const defaultApplierBudget = 10 * time.Second

// hostCascadeDeleteBudget is the (much larger) deadline for applyHostDelete.
// Deleting a host can purge hundreds of thousands of docker_container_entities
// rows; even chunked, that legitimately takes far longer than a metric write,
// and the default 10s deadline would cancel it mid-cascade on every replica.
const hostCascadeDeleteBudget = 120 * time.Second

// applierCtx returns a context flagged as an applier so any future
// WriteGate plumbing recognises the call site as legitimate, plus the default
// deadline.
func (a *appliers) applierCtx() (context.Context, context.CancelFunc) {
	return a.applierCtxWithBudget(defaultApplierBudget)
}

// applierCtxWithBudget is applierCtx with an explicit deadline for the rare
// long-running appliers.
func (a *appliers) applierCtxWithBudget(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(WithApplier(context.Background()), d)
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
	if a.remoteOrigin(cmd) != "" {
		// Cross-site MAC collision guard: docker-bridge agents on different
		// machines derive identical MACs — never merge a foreign machine
		// into THIS node's own identity row. The remote site's data still
		// reaches us through its connector rows.
		if existing, err := a.deps.HostRepo.GetHostByMacAddress(ctx, p.MacAddress); err == nil &&
			existing.ID == hosts.LocalCollectorHostID {
			return nil
		}
	}
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

func (a *appliers) applyGatewayRouteUpsert(cmd Command, _ *hraft.Log) error {
	var p GatewayRouteUpsertPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.RouteID == "" {
		return errors.New("raft: GatewayRouteUpsert requires route_id")
	}
	if a.deps.GatewayRepo == nil {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.GatewayRepo.Upsert(ctx, &gateway.Route{
		RouteID:                  p.RouteID,
		Name:                     p.Name,
		Domain:                   p.Domain,
		PathPrefix:               p.PathPrefix,
		TargetScheme:             p.TargetScheme,
		TargetHost:               p.TargetHost,
		TargetPort:               p.TargetPort,
		TargetHostMAC:            p.TargetHostMAC,
		TargetLabel:              p.TargetLabel,
		TargetInsecureSkipVerify: p.TargetInsecureSkipVerify,
		Mode:                     p.Mode,
		TargetHTTPSPort:          p.TargetHTTPSPort,
		TLS:                      p.TLS,
		BasicAuthUsers:           p.BasicAuthUsers,
		IPAllowList:              p.IPAllowList,
		Enabled:                  p.Enabled,
		CreatedAt:                cmd.Timestamp,
	})
}

func (a *appliers) applyGatewayRouteDelete(cmd Command, _ *hraft.Log) error {
	var p GatewayRouteDeletePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.RouteID == "" {
		return errors.New("raft: GatewayRouteDelete requires route_id")
	}
	if a.deps.GatewayRepo == nil {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.GatewayRepo.DeleteByRouteID(ctx, p.RouteID)
}

func (a *appliers) applyHostDelete(cmd Command, _ *hraft.Log) error {
	var p HostDeletePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.HostMAC == "" {
		return nil
	}
	// A host cascade can purge hundreds of thousands of rows — give it a far
	// larger budget than a normal applier write.
	ctx, cancel := a.applierCtxWithBudget(hostCascadeDeleteBudget)
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

// applyMetricBatch persists a replicated host's metrics. Metrics no longer flow
// through the durable Raft log in normal operation (they ride the best-effort
// metric stream instead — see MetricSink); this applier is retained only so a
// replayed legacy CmdMetricBatch entry from an old log still applies. It shares
// the exact persist core with the live stream via MetricSink.
func (a *appliers) applyMetricBatch(cmd Command, _ *hraft.Log) error {
	var p MetricBatchPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.metricSink.Ingest(ctx, p, a.remoteOrigin(cmd))
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
	// password changes ride their own CmdUserPasswordChange (applyUserPasswordChange).
	if p.Role != "" && existing.Role != p.Role {
		return a.deps.UserRepo.UpdateRole(ctx, existing.ID, p.Role)
	}
	return nil
}

func (a *appliers) applyUserPasswordChange(cmd Command, _ *hraft.Log) error {
	var p UserPasswordChangePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	ctx, cancel := a.applierCtx()
	defer cancel()

	existing, err := a.deps.UserRepo.FindByEmail(ctx, p.Email)
	if err != nil {
		return err
	}
	if existing == nil {
		// Nothing to update on this replica yet — a CmdUserUpsert for this
		// email orders before this command in the log, so a missing row here
		// means the account was deleted. Treat as a no-op.
		return nil
	}
	return a.deps.UserRepo.UpdatePassword(ctx, existing.ID, p.PasswordHash)
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

func (a *appliers) applyGatewayBlockUpsert(cmd Command, _ *hraft.Log) error {
	var p GatewayBlockUpsertPayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.BlockID == "" {
		return errors.New("raft: GatewayBlockUpsert requires block_id")
	}
	if a.deps.GatewayBlockRepo == nil {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.GatewayBlockRepo.UpsertBlock(ctx, &gateway.Block{
		BlockID:   p.BlockID,
		CIDR:      p.CIDR,
		Reason:    p.Reason,
		Source:    p.Source,
		CreatedBy: p.CreatedBy,
		ExpiresAt: p.ExpiresAt,
		CreatedAt: cmd.Timestamp,
	})
}

func (a *appliers) applyGatewayBlockDelete(cmd Command, _ *hraft.Log) error {
	var p GatewayBlockDeletePayload
	if err := DecodeTyped(cmd, &p); err != nil {
		return err
	}
	if p.BlockID == "" {
		return errors.New("raft: GatewayBlockDelete requires block_id")
	}
	if a.deps.GatewayBlockRepo == nil {
		return nil
	}
	ctx, cancel := a.applierCtx()
	defer cancel()
	return a.deps.GatewayBlockRepo.DeleteBlockByBlockID(ctx, p.BlockID)
}
