package raft

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// BootstrapClusterSecrets, called on every node at startup, ensures that
// the cluster-shared JWT signing secrets are present in cluster_config.
// Behaviour:
//
//   - If cluster_config already has both keys → return them (so callers can
//     override the env-loaded secrets, making sessions valid on every node).
//   - Otherwise, if this node is the Raft leader → submit CmdAuthSecretSet
//     using the env-provided secrets and return them.
//   - Otherwise → return ("", "", nil) and let the caller stick with env.
//     Once the leader publishes, the eventual snapshot replay will fill in.
func BootstrapClusterSecrets(ctx context.Context, logger *log.Logger, svc Service, replicator *Replicator, db *gorm.DB, envJWT, envRefresh string) (jwtSecret, refreshSecret string, err error) {
	if svc == nil || !svc.Enabled() || db == nil {
		return envJWT, envRefresh, nil
	}
	gotJWT, _ := LookupClusterConfig(ctx, db, "jwt_secret")
	gotRefresh, _ := LookupClusterConfig(ctx, db, "refresh_secret")
	if gotJWT != "" && gotRefresh != "" {
		if logger != nil {
			logger.Info("raft: using cluster-shared auth secrets from FSM")
		}
		return gotJWT, gotRefresh, nil
	}
	if !svc.IsLeader() || replicator == nil {
		// Followers wait for the leader. Use env in the meantime so the
		// process can still start; sessions minted now will be valid on
		// this node only until the secrets replicate.
		return envJWT, envRefresh, nil
	}
	if envJWT == "" || envRefresh == "" {
		return envJWT, envRefresh, nil
	}
	if err := replicator.SubmitAuthSecretSet(ctx, envJWT, envRefresh); err != nil {
		return envJWT, envRefresh, err
	}
	if logger != nil {
		logger.Info("raft: seeded cluster auth secrets from local env")
	}
	return envJWT, envRefresh, nil
}

// AdvertiseSelf publishes this node's advertised URL into the URL catalog
// (CmdPeerNodeAdvertise). Best-effort: a transient error here is recorded
// but does not block startup — the catalog updates again on every restart.
func AdvertiseSelf(ctx context.Context, logger *log.Logger, svc Service, replicator *Replicator, clusterID, nodeID, advertiseURL string) {
	if svc == nil || !svc.Enabled() || replicator == nil || advertiseURL == "" {
		return
	}
	if !svc.IsLeader() {
		// Only the leader writes; followers' identical CmdPeerNodeAdvertise
		// would race anyway. The leader's submission propagates to all
		// replicas, including the follower itself.
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := replicator.SubmitPeerNodeAdvertise(ctx, clusterID, nodeID, advertiseURL, nil); err != nil {
		if logger != nil {
			logger.Warn("raft: peer advertise failed", "error", err)
		}
	}
}
