package connectors

import (
	"context"
	"os"
	"strings"
	"time"
)

// Proxmox auto-connect env vars (set by the Proxmox-LXC installer on the PVE
// host). When all three credentials are present the node self-attaches to its
// hypervisor on first boot — no manual Connectors step in the UI.
const (
	envProxmoxAutoConnect = "PROXMOX_AUTOCONNECT"         // "0"/"false"/"off" disables (default on)
	envProxmoxURL         = "NODE_STATS_PROXMOX_URL"      // e.g. https://192.168.1.2:8006
	envProxmoxTokenID     = "NODE_STATS_PROXMOX_TOKEN_ID" // user@realm!tokenid
	envProxmoxSecret      = "NODE_STATS_PROXMOX_TOKEN_SECRET"
	envProxmoxSkipTLS     = "NODE_STATS_PROXMOX_SKIP_TLS_VERIFY" // default true (PVE ships a self-signed cert)
)

// envDisabled reports whether an env flag is explicitly turned off.
func envDisabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// BootstrapProxmoxFromEnv auto-creates a Proxmox connector from the
// NODE_STATS_PROXMOX_* environment variables, then triggers a poller resync.
// It is:
//   - opt-out: skipped when PROXMOX_AUTOCONNECT is 0/false/off, or when any of
//     the URL / token id / secret env vars is empty (nothing to connect);
//   - idempotent: a no-op when a Proxmox connector already exists (so it never
//     clobbers a hand-configured one or rotates the secret on a re-run);
//   - non-fatal: the live probe may race the container's network/PVE API
//     coming up, so it retries with backoff and, on persistent failure, only
//     logs a warning — the admin can always add the connector in the UI.
//
// The caller must ensure JWT_SECRET is set before invoking this (the token is
// encrypted under a key derived from it); otherwise the wizard would later
// rotate the secret and orphan the stored ciphertext.
func (s *service) BootstrapProxmoxFromEnv(ctx context.Context) {
	if envDisabled(envProxmoxAutoConnect) {
		return
	}
	endpoint := strings.TrimSpace(os.Getenv(envProxmoxURL))
	tokenID := strings.TrimSpace(os.Getenv(envProxmoxTokenID))
	secret := strings.TrimSpace(os.Getenv(envProxmoxSecret))
	if endpoint == "" || tokenID == "" || secret == "" {
		return
	}

	// Idempotent: never touch an existing Proxmox connector.
	if existing, err := s.repo.List(ctx); err == nil {
		for _, c := range existing {
			if c.Type == TypeProxmox {
				s.logger.Debug("proxmox auto-connect: a connector is already configured, skipping")
				return
			}
		}
	}

	req := ConnectRequest{
		Endpoint:      endpoint,
		TokenID:       tokenID,
		Secret:        secret,
		SkipTLSVerify: !envDisabled(envProxmoxSkipTLS), // default true
	}

	s.logger.Info("proxmox auto-connect: attaching to hypervisor", "endpoint", endpoint, "token_id", tokenID)
	var lastErr error
	for attempt := 1; attempt <= 6; attempt++ {
		conn, err := s.CreateProxmox(ctx, req)
		if err == nil {
			s.logger.Info("proxmox auto-connect: connector created", "endpoint", endpoint, "fingerprint", conn.Fingerprint)
			return
		}
		lastErr = err
		// Backoff 1,4,9,16,25s — gives the network / PVE API time to settle.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt*attempt) * time.Second):
		}
	}
	s.logger.Warn("proxmox auto-connect failed; add the connector manually in the UI",
		"endpoint", endpoint, "error", lastErr)
}
