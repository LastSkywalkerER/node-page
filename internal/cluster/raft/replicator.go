package raft

import (
	"context"
	"time"

	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
	connectors "system-stats/internal/platform/connectors"
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

// SubmitMetricBatch replicates one host's current metrics to the whole
// cluster so every node can serve that host's stats (and they survive the
// origin going offline). Best-effort: callers ignore the error so a missing
// quorum never stalls the local collection cycle.
func (r *Replicator) SubmitMetricBatch(ctx context.Context, p MetricBatchPayload) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdMetricBatch, p, 5*time.Second)
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
