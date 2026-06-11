package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

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
		return nil, wrapAuth(err, tokenID)
	}
	status, err := client.ClusterStatus(ctx)
	if err != nil {
		return nil, wrapAuth(err, tokenID)
	}
	fingerprint := Fingerprint(status)
	if fingerprint == "" {
		return nil, fmt.Errorf("proxmox: /cluster/status returned no node — token lacks Sys.Audit?")
	}
	clusterName := fingerprint
	// A PVE *cluster* name identifies the whole cluster no matter which node's
	// endpoint the admin entered, so it stays as-is (re-connecting via another
	// node updates the same connector). A *standalone* node is named "pve" on
	// every default install — qualify it with the endpoint host so several
	// independent Proxmoxes can be added side by side without colliding.
	if strings.HasPrefix(fingerprint, "node/") {
		if u, uerr := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/")); uerr == nil && u.Host != "" {
			fingerprint += "@" + u.Host
		}
	}
	resources, err := client.ClusterResources(ctx)
	if err != nil {
		return nil, wrapAuth(err, tokenID)
	}

	var nodes []string
	guestCount := 0
	var guestMACs, guestUUIDs []string
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
				if u := SMBIOSUUID(cfg); u != "" {
					guestUUIDs = append(guestUUIDs, u)
				}
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
		GuestUUIDs:  guestUUIDs,
	}, nil
}

func wrapAuth(err error, tokenID string) error {
	var ae *AuthError
	if !errors.As(err, &ae) {
		return err
	}
	// PVE puts the denial reason in the HTTP status line. "Permission check
	// failed" means the token AUTHENTICATED but has no privileges — the classic
	// Privilege Separation pitfall: a privsep token (the default) inherits
	// nothing from its user, even root@pam, until an ACL names the token.
	if strings.Contains(ae.Status, "Permission check failed") {
		return fmt.Errorf(
			"%w: %s. The token is valid but has no privileges — with Privilege Separation enabled (the Proxmox default) permissions must be granted to the token itself, not just the user. On the PVE node run:\npveum acl modify / -token '%s' -role PVEAuditor\nor in the UI: Datacenter → Permissions → Add → API Token Permission → path /, token %s, role PVEAuditor",
			connectors.ErrAuthFailed, ae.Error(), tokenID, tokenID,
		)
	}
	return fmt.Errorf("%w: %s", connectors.ErrAuthFailed, ae.Error())
}
