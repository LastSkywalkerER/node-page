package proxmox

import (
	"context"
	"errors"
	"fmt"
	"sort"

	connectors "system-stats/internal/platform/connectors"
)

// Prober implements connectors.Prober for TypeProxmox: validates credentials,
// fingerprints the cluster and previews the topology.
type Prober struct{}

// NewProber creates the Proxmox credential prober.
func NewProber() *Prober { return &Prober{} }

// Probe validates the endpoint + token and returns the connect preview.
func (p *Prober) Probe(ctx context.Context, endpoint, tokenID, secret string, skipTLSVerify bool) (*connectors.ProbeResult, error) {
	client, err := NewClient(endpoint, tokenID, secret, skipTLSVerify)
	if err != nil {
		return nil, err
	}
	version, err := client.Version(ctx)
	if err != nil {
		return nil, wrapAuth(err)
	}
	status, err := client.ClusterStatus(ctx)
	if err != nil {
		return nil, wrapAuth(err)
	}
	fingerprint := Fingerprint(status)
	if fingerprint == "" {
		return nil, fmt.Errorf("proxmox: /cluster/status returned no node — token lacks Sys.Audit?")
	}
	clusterName := fingerprint
	resources, err := client.ClusterResources(ctx)
	if err != nil {
		return nil, wrapAuth(err)
	}

	var nodes []string
	guestCount := 0
	var guestMACs []string
	for _, r := range resources {
		switch r.Type {
		case "node":
			nodes = append(nodes, r.Node)
		case "qemu", "lxc":
			if r.Template == 1 {
				continue
			}
			guestCount++
			if cfg, cerr := client.GuestConfig(ctx, r.Node, r.Type, r.VMID); cerr == nil {
				guestMACs = append(guestMACs, ConfigMACs(cfg)...)
			}
		}
	}
	sort.Strings(nodes)

	return &connectors.ProbeResult{
		Fingerprint: fingerprint,
		ClusterName: clusterName,
		Version:     version,
		Nodes:       nodes,
		GuestCount:  guestCount,
		GuestMACs:   guestMACs,
	}, nil
}

func wrapAuth(err error) error {
	var ae *AuthError
	if errors.As(err, &ae) {
		return fmt.Errorf("%w: %s", connectors.ErrAuthFailed, ae.Error())
	}
	return err
}
