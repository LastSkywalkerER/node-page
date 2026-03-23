package application

import (
	"context"
	"errors"

	hostentities "system-stats/internal/modules/hosts/infrastructure/entities"
	clusterconfig "system-stats/internal/modules/nodes/infrastructure/cluster_config"
)

// ErrCannotDeleteLocalHost is returned when DELETE targets the host running this server.
var ErrCannotDeleteLocalHost = errors.New("cannot delete local host")

// ClusterUIStatus drives the Nodes admin screen (connect block visibility, push URL).
type ClusterUIStatus struct {
	ShowConnectBlock bool
	PushURL          string
	IsAgent          bool
	HasRemoteAgents  bool
	// When IsAgent: main URL and token from runtime config (for admin UI only).
	AgentMainNodeURL    string
	AgentNodeAccessToken string
}

// GetClusterUIStatus returns whether to show "Connect this node" and the push URL for agents.
// Connect is hidden when this instance is already an agent (MAIN_NODE_URL + token) or when
// at least one other host has a push credential on this main.
func (s *service) GetClusterUIStatus(ctx context.Context, currentHostID uint, publicBaseURL string) (ClusterUIStatus, error) {
	mainURL, token := clusterconfig.Get()
	isAgent := mainURL != "" && token != ""

	n, err := s.credRepo.CountWhereHostIDNot(ctx, currentHostID)
	if err != nil {
		return ClusterUIStatus{}, err
	}
	hasRemote := n > 0

	showConnect := !(isAgent || hasRemote)
	pushURL := publicBaseURL + "/api/v1/nodes/push"

	st := ClusterUIStatus{
		ShowConnectBlock: showConnect,
		PushURL:          pushURL,
		IsAgent:          isAgent,
		HasRemoteAgents:  hasRemote,
	}
	if isAgent {
		st.AgentMainNodeURL = mainURL
		st.AgentNodeAccessToken = token
	}
	return st, nil
}

// DeleteRemoteHost removes a remote host and all metrics/credentials for it. Fails for the local host ID.
func (s *service) DeleteRemoteHost(ctx context.Context, hostID, currentHostID uint) error {
	if hostID == 0 || hostID == currentHostID {
		return ErrCannotDeleteLocalHost
	}
	if hostID == hostentities.LocalCollectorHostID {
		return ErrCannotDeleteLocalHost
	}
	if _, err := s.hostRepo.GetHostByID(ctx, hostID); err != nil {
		return err
	}
	return s.hostRepo.DeleteHostCascade(ctx, hostID)
}
