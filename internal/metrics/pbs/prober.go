package pbs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	connectors "system-stats/internal/platform/connectors"
)

// Prober implements connectors.Prober for TypePBS: validates credentials and
// fingerprints the backup server.
type Prober struct{}

// NewProber creates the PBS credential prober.
func NewProber() *Prober { return &Prober{} }

// Probe validates the endpoint + token and returns the connect preview. PBS has
// no cluster concept, so the fingerprint is qualified by the endpoint host
// ("pbs/<host>") — letting several independent backup servers be added
// side by side, and re-connecting the same one (same host) updates it in place.
func (p *Prober) Probe(ctx context.Context, endpoint, tokenID, secret string, skipTLSVerify bool) (*connectors.ProbeResult, error) {
	client, err := NewClient(endpoint, tokenID, secret, skipTLSVerify)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	version, err := client.Version(ctx)
	if err != nil {
		return nil, wrapAuth(err, tokenID)
	}
	node := client.NodeName(ctx)
	// Confirm the token actually has read privileges (Version can succeed with a
	// privsep token that has no ACL) by hitting a privileged endpoint.
	if _, err := client.NodeStatus(ctx, node); err != nil {
		return nil, wrapAuth(err, tokenID)
	}
	stores, err := client.DatastoreUsage(ctx)
	if err != nil {
		return nil, wrapAuth(err, tokenID)
	}

	fingerprint := "pbs/" + client.Host()
	return &connectors.ProbeResult{
		Fingerprint: fingerprint,
		ClusterName: client.Host(),
		Version:     version,
		Nodes:       []string{node},
		GuestCount:  len(stores), // datastores, surfaced in the preview's count
	}, nil
}

func wrapAuth(err error, tokenID string) error {
	var ae *AuthError
	if !errors.As(err, &ae) {
		return err
	}
	if strings.Contains(ae.Status, "Permission check failed") {
		return fmt.Errorf(
			"%w: %s. The token is valid but has no privileges — with Privilege Separation enabled (the PBS default) permissions must be granted to the token itself. In the PBS UI: Configuration → Access Control → Permissions → Add → API Token Permission → path /, token %s, role Audit",
			connectors.ErrAuthFailed, ae.Error(), tokenID,
		)
	}
	return fmt.Errorf("%w: %s", connectors.ErrAuthFailed, ae.Error())
}
