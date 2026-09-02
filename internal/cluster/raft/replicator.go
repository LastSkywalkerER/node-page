package raft

import (
	"context"
	"time"

	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	connectors "system-stats/internal/platform/connectors"
	gateway "system-stats/internal/platform/gateway"
)

// Replicator adapts the generic Service to the small, domain-specific
// interfaces that individual modules (hosts, users, …) depend on. Modules
// import only their own interface (hosts.RaftReplicator etc.) and stay
// decoupled from this package, avoiding import cycles.
type Replicator struct {
	svc Service
}

// NewReplicator wires the underlying Service.
func NewReplicator(svc Service) *Replicator { return &Replicator{svc: svc} }

// Enabled reports whether the underlying Raft layer is active.
func (r *Replicator) Enabled() bool {
	return r != nil && r.svc != nil && r.svc.Enabled()
}

// SubmitHostUpsert publishes a CmdHostUpsert for the given hostInfo.
func (r *Replicator) SubmitHostUpsert(ctx context.Context, info hosts.HostInfo) error {
	if !r.Enabled() {
		return nil
	}
	payload := HostUpsertPayload{
		Name:                 info.Name,
		MacAddress:           info.MacAddress,
		IPv4:                 info.IPv4,
		OS:                   info.OS,
		Platform:             info.Platform,
		PlatformFamily:       info.PlatformFamily,
		PlatformVersion:      info.PlatformVersion,
		KernelVersion:        info.KernelVersion,
		VirtualizationSystem: info.VirtualizationSystem,
		VirtualizationRole:   info.VirtualizationRole,
		HostID:               info.HostID,
		HardwareUUID:         info.HardwareUUID,
		BootTime:             info.BootTime,
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostUpsert, payload, 5*time.Second)
	return err
}

// SubmitConnectorHostUpsert publishes a connector-discovered host (hypervisor
// node or agent-less guest) incl. its topology fields.
func (r *Replicator) SubmitConnectorHostUpsert(ctx context.Context, info hosts.ConnectorHostInfo) error {
	if !r.Enabled() {
		return nil
	}
	payload := ConnectorHostUpsertPayload{
		HostUpsertPayload: HostUpsertPayload{
			Name:                 info.Name,
			MacAddress:           info.MacAddress,
			IPv4:                 info.IPv4,
			OS:                   info.OS,
			Platform:             info.Platform,
			PlatformFamily:       info.PlatformFamily,
			PlatformVersion:      info.PlatformVersion,
			KernelVersion:        info.KernelVersion,
			VirtualizationSystem: info.VirtualizationSystem,
			VirtualizationRole:   info.VirtualizationRole,
			HostID:               info.HostID,
			HardwareUUID:         info.HardwareUUID,
			BootTime:             info.BootTime,
		},
		HostType:    info.HostType,
		ParentMAC:   info.ParentMAC,
		ExternalID:  info.ExternalID,
		GuestStatus: info.GuestStatus,
	}
	_, err := SubmitTyped(ctx, r.svc, CmdConnectorHostUpsert, payload, 5*time.Second)
	return err
}

// SubmitHostPendingUpsert replicates a frozen host-identity proposal (or its
// rejected status), keyed by ChangeID.
func (r *Replicator) SubmitHostPendingUpsert(ctx context.Context, ch hosts.HostPendingChange) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostPendingUpsert, HostPendingUpsertPayload{
		ChangeID:    ch.ChangeID,
		HostMAC:     ch.HostMAC,
		HostName:    ch.HostName,
		Source:      ch.Source,
		Changes:     []byte(ch.Changes),
		Fingerprint: ch.Fingerprint,
		Status:      ch.Status,
	}, 5*time.Second)
	return err
}

// SubmitHostPendingDelete removes a proposal on every node.
func (r *Replicator) SubmitHostPendingDelete(ctx context.Context, changeID string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostPendingDelete, HostPendingDeletePayload{ChangeID: changeID}, 5*time.Second)
	return err
}

// SubmitHostPendingApply applies an approved proposal on every node.
func (r *Replicator) SubmitHostPendingApply(ctx context.Context, changeID string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostPendingApply, HostPendingApplyPayload{ChangeID: changeID}, 5*time.Second)
	return err
}

// SubmitConnectorUpsert replicates a configured connector cluster-wide.
func (r *Replicator) SubmitConnectorUpsert(ctx context.Context, c connectors.Connector) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdConnectorUpsert, ConnectorUpsertPayload{
		Type:          c.Type,
		Endpoint:      c.Endpoint,
		TokenID:       c.TokenID,
		SecretEnc:     c.SecretEnc,
		SkipTLSVerify: c.SkipTLSVerify,
		Fingerprint:   c.Fingerprint,
		Config:        c.Config,
		Enabled:       c.Enabled,
	}, 5*time.Second)
	return err
}

// SubmitGatewayRouteUpsert replicates a gateway route cluster-wide.
func (r *Replicator) SubmitGatewayRouteUpsert(ctx context.Context, g gateway.Route) error {
	if !r.Enabled() {
		return nil
	}
	res, err := SubmitTyped(ctx, r.svc, CmdGatewayRouteUpsert, GatewayRouteUpsertPayload{
		RouteID:                  g.RouteID,
		Name:                     g.Name,
		Domain:                   g.Domain,
		PathPrefix:               g.PathPrefix,
		TargetScheme:             g.TargetScheme,
		TargetHost:               g.TargetHost,
		TargetPort:               g.TargetPort,
		TargetHostMAC:            g.TargetHostMAC,
		TargetLabel:              g.TargetLabel,
		TargetInsecureSkipVerify: g.TargetInsecureSkipVerify,
		Mode:                     g.Mode,
		TargetHTTPSPort:          g.TargetHTTPSPort,
		TLS:                      g.TLS,
		BasicAuthUsers:           g.BasicAuthUsers,
		IPAllowList:              g.IPAllowList,
		MaxConnsPerIP:            g.MaxConnsPerIP,
		RateLimitRPS:             g.RateLimitRPS,
		ReadOnly:                 g.ReadOnly,
		UpstreamTimeoutSeconds:   g.UpstreamTimeoutSeconds,
		MaxBodyBytes:             g.MaxBodyBytes,
		Enabled:                  g.Enabled,
	}, 5*time.Second)
	return firstErr(err, res.Err)
}

// SubmitGatewayRouteDelete removes a gateway route on every node.
func (r *Replicator) SubmitGatewayRouteDelete(ctx context.Context, routeID string) error {
	if !r.Enabled() {
		return nil
	}
	res, err := SubmitTyped(ctx, r.svc, CmdGatewayRouteDelete, GatewayRouteDeletePayload{RouteID: routeID}, 5*time.Second)
	return firstErr(err, res.Err)
}

// SubmitGatewayBlockUpsert replicates a gateway client block cluster-wide.
func (r *Replicator) SubmitGatewayBlockUpsert(ctx context.Context, b gateway.Block) error {
	if !r.Enabled() {
		return nil
	}
	res, err := SubmitTyped(ctx, r.svc, CmdGatewayBlockUpsert, GatewayBlockUpsertPayload{
		BlockID:   b.BlockID,
		CIDR:      b.CIDR,
		Reason:    b.Reason,
		Source:    b.Source,
		CreatedBy: b.CreatedBy,
		ExpiresAt: b.ExpiresAt,
	}, 5*time.Second)
	return firstErr(err, res.Err)
}

// SubmitGatewayBlockDelete removes a gateway block on every node.
func (r *Replicator) SubmitGatewayBlockDelete(ctx context.Context, blockID string) error {
	if !r.Enabled() {
		return nil
	}
	res, err := SubmitTyped(ctx, r.svc, CmdGatewayBlockDelete, GatewayBlockDeletePayload{BlockID: blockID}, 5*time.Second)
	return firstErr(err, res.Err)
}

// BackfillLocalGatewayRoutes republishes routes that only live in this node's
// local DB (created while standalone) — same rationale as BackfillLocalConnectors.
func (r *Replicator) BackfillLocalGatewayRoutes(ctx context.Context, repo gateway.Repository) (int, error) {
	if !r.Enabled() {
		return 0, nil
	}
	rows, err := repo.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, g := range rows {
		if g.RouteID == "" {
			continue
		}
		if err := r.SubmitGatewayRouteUpsert(ctx, g); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// SubmitConnectorDelete removes a connector (and optionally its
// connector-only host rows) on every node.
func (r *Replicator) SubmitConnectorDelete(ctx context.Context, connectorType, fingerprint string, removeHosts bool) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdConnectorDelete, ConnectorDeletePayload{
		Type:        connectorType,
		Fingerprint: fingerprint,
		RemoveHosts: removeHosts,
	}, 5*time.Second)
	return err
}

// SubmitHostDelete cascades a host delete (row + all its metrics) across the
// cluster, keyed by the host's MAC so every node removes the right local row.
func (r *Replicator) SubmitHostDelete(ctx context.Context, mac string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostDelete, HostDeletePayload{HostMAC: mac}, 5*time.Second)
	return err
}

// SubmitUserUpsert publishes a CmdUserUpsert.
func (r *Replicator) SubmitUserUpsert(ctx context.Context, email, passwordHash, role string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdUserUpsert, UserUpsertPayload{
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
	}, 5*time.Second)
	return err
}

// SubmitUserPasswordChange publishes a CmdUserPasswordChange for an existing user.
func (r *Replicator) SubmitUserPasswordChange(ctx context.Context, email, passwordHash string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdUserPasswordChange, UserPasswordChangePayload{
		Email:        email,
		PasswordHash: passwordHash,
	}, 5*time.Second)
	return err
}

// SubmitAuthSecretSet publishes the cluster-shared JWT signing keys.
func (r *Replicator) SubmitAuthSecretSet(ctx context.Context, jwtSecret, refreshSecret string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdAuthSecretSet, AuthSecretSetPayload{
		JWTSecret:     jwtSecret,
		RefreshSecret: refreshSecret,
	}, 5*time.Second)
	return err
}

// SubmitConfigSet replicates a single key/value into the cluster_config table
// (CmdConfigSet) so every node converges on it. Used to share the cross-cluster
// bridge uplink config (secret / hub seeds / mode) so each spoke node can ship
// its OWN metrics to the hub. Never crosses the bridge (see crossClusterDeny).
func (r *Replicator) SubmitConfigSet(ctx context.Context, key, value string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdConfigSet, ConfigSetPayload{
		Key:   key,
		Value: value,
	}, 5*time.Second)
	return err
}

// SubmitPeerNodeAdvertise publishes this node's advertised URL.
func (r *Replicator) SubmitPeerNodeAdvertise(ctx context.Context, clusterID, nodeID, url string, capabilities []string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdPeerNodeAdvertise, PeerNodeAdvertisePayload{
		ClusterID:    clusterID,
		NodeID:       nodeID,
		URL:          url,
		Capabilities: capabilities,
	}, 5*time.Second)
	return err
}

// SubmitPeerNodeRemove drops a node from the replicated URL catalog
// (peer_node_advertise) so it stops being a write-forward target and a bridge
// picker probe candidate. Used when a peer is removed from the cluster so the
// removal is complete (the Raft config change alone leaves the catalog row).
func (r *Replicator) SubmitPeerNodeRemove(ctx context.Context, clusterID, nodeID string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdPeerNodeRemove, PeerNodeRemovePayload{
		ClusterID: clusterID,
		NodeID:    nodeID,
	}, 5*time.Second)
	return err
}

// SubmitJoinTokenIssue records a new bootstrap token.
func (r *Replicator) SubmitJoinTokenIssue(ctx context.Context, tokenHash string, expiresAt time.Time, createdBy uint) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdJoinTokenIssue, JoinTokenIssuePayload{
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedBy: createdBy,
	}, 5*time.Second)
	return err
}

// SubmitJoinTokenConsume marks a join token used by the given node.
func (r *Replicator) SubmitJoinTokenConsume(ctx context.Context, tokenHash, byNodeID, byAddr string) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdJoinTokenConsume, JoinTokenConsumePayload{
		TokenHash:  tokenHash,
		ByNodeID:   byNodeID,
		ByNodeAddr: byAddr,
	}, 5*time.Second)
	return err
}

// BackfillLocalHosts walks the local hosts table and submits a
// CmdHostUpsert for every row. Used right after activation so that
// each node's host info (the row inserted at boot via UpsertLocalHost,
// before Raft was active) gets into the replicated log and shows up
// on every other node's dashboard. The applier dedupes by MAC.
func (r *Replicator) BackfillLocalHosts(ctx context.Context, hostRepo hosts.Repository) (int, error) {
	if !r.Enabled() {
		return 0, nil
	}
	all, err := hostRepo.GetAllHosts(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, h := range all {
		if h.MacAddress == "" {
			continue
		}
		info := hosts.HostInfo{
			Name:                 h.Name,
			MacAddress:           h.MacAddress,
			IPv4:                 h.IPv4,
			OS:                   h.OS,
			Platform:             h.Platform,
			PlatformFamily:       h.PlatformFamily,
			PlatformVersion:      h.PlatformVersion,
			KernelVersion:        h.KernelVersion,
			VirtualizationSystem: h.VirtualizationSystem,
			VirtualizationRole:   h.VirtualizationRole,
			HostID:               h.SystemHostID,
			HardwareUUID:         h.HardwareUUID,
		}
		// Connector-only rows must keep their topology and must NOT be
		// republished as agent rows (that would flip their source and fake
		// their liveness on peers).
		if h.Source == hosts.SourceConnector {
			err = r.SubmitConnectorHostUpsert(ctx, hosts.ConnectorHostInfo{
				HostInfo:    info,
				HostType:    h.HostType,
				ParentMAC:   h.ParentMAC,
				ExternalID:  h.ExternalID,
				GuestStatus: h.GuestStatus,
			})
		} else {
			err = r.SubmitHostUpsert(ctx, info)
			// Agent rows that gained topology (agent+connector — e.g. a node
			// self-linked to its own PVE guest) must re-ship it too: the
			// connector poller publishes topology only when it CHANGES, so a
			// peer that missed that one event (uplink hub enrolled later, or
			// entries dropped during an outage) would keep this host detached
			// from its hypervisor forever.
			if err == nil && h.ExternalID != "" {
				err = r.SubmitConnectorHostUpsert(ctx, hosts.ConnectorHostInfo{
					HostInfo:    info,
					HostType:    h.HostType,
					ParentMAC:   h.ParentMAC,
					ExternalID:  h.ExternalID,
					GuestStatus: h.GuestStatus,
				})
			}
		}
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// BackfillLocalUsers walks the local users table and submits a
// CmdUserUpsert for every user. The applier's FindByEmail + role-update
// logic dedupes safely so re-running the backfill is a no-op on
// replicas that already have the user. Used right after a fresh
// activation to ensure any users created during the bootstrap election
// window (when the node was briefly Candidate and SubmitCommand
// returned ErrNotLeader) end up in the Raft log.
func (r *Replicator) BackfillLocalUsers(ctx context.Context, userRepo users.UserRepository) (int, error) {
	if !r.Enabled() {
		return 0, nil
	}
	const pageSize = 100
	count := 0
	for offset := 0; ; offset += pageSize {
		page, err := userRepo.List(ctx, offset, pageSize)
		if err != nil {
			return count, err
		}
		if len(page) == 0 {
			break
		}
		for _, u := range page {
			if err := r.SubmitUserUpsert(ctx, u.Email, u.PasswordHash, u.Role); err != nil {
				// Stop early on ErrNotLeader / ErrDisabled — there's
				// no point hammering. Other errors (e.g. validation
				// inside the applier) are deterministic and we log
				// them by returning.
				return count, err
			}
			count++
		}
		if len(page) < pageSize {
			break
		}
	}
	return count, nil
}

// BackfillLocalConnectors walks the local connectors table and republishes every
// row into the replicated log via CmdConnectorUpsert (forwarded to the leader
// when this node is a follower). Unlike the user/host backfills this is NOT
// leader-gated: a connector configured on a node while it was standalone (or
// that only ever lived in that node's local SQLite — the connector CRUD's
// persistUpsert is Raft-only once clustered, so a connector created before the
// cluster formed never entered the log) must reach every peer so ANY node can
// poll it after a failover. The applier dedupes by fingerprint, so re-running is
// a harmless no-op on peers that already have the row.
func (r *Replicator) BackfillLocalConnectors(ctx context.Context, connRepo connectors.Repository) (int, error) {
	if !r.Enabled() {
		return 0, nil
	}
	rows, err := connRepo.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, c := range rows {
		if c.Fingerprint == "" {
			continue
		}
		if err := r.SubmitConnectorUpsert(ctx, c); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// firstErr returns the submit error if any, else the applier's error. The
// applier error (SubmitResult.Err) is the FSM's verdict — a command that was
// committed but failed to persist (e.g. a SQL error) must not look like a
// success to the caller, or the UI reports "done" for a row that never landed.
func firstErr(submit, applied error) error {
	if submit != nil {
		return submit
	}
	return applied
}
