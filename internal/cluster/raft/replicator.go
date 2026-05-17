package raft

import (
	"context"
	"time"

	users "system-stats/internal/auth/users"
	hosts "system-stats/internal/cluster/hosts"
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
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostUpsert, payload, 5*time.Second)
	return err
}

// SubmitHostLastSeen publishes a CmdHostLastSeen for a node-push heartbeat.
func (r *Replicator) SubmitHostLastSeen(ctx context.Context, hostID uint, lastSeen time.Time, agentSession *time.Time, name, ipv4 string) error {
	if !r.Enabled() {
		return nil
	}
	payload := HostLastSeenPayload{
		HostID:                hostID,
		LastSeen:              lastSeen,
		AgentSessionStartedAt: agentSession,
		Name:                  name,
		IPv4:                  ipv4,
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostLastSeen, payload, 3*time.Second)
	return err
}

// SubmitHostDelete cascades a host delete across the cluster.
func (r *Replicator) SubmitHostDelete(ctx context.Context, hostID uint) error {
	if !r.Enabled() {
		return nil
	}
	_, err := SubmitTyped(ctx, r.svc, CmdHostDelete, HostDeletePayload{HostID: hostID}, 5*time.Second)
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
