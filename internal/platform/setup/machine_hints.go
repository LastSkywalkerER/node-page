package setup

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v4/host"

	hostnet "system-stats/internal/platform/hostnet"
)

// MachineHints are best-effort values for the setup wizard (no MAC / DB required).
type MachineHints struct {
	SuggestedHostname string `json:"suggested_hostname"`
	SuggestedIPv4     string `json:"suggested_ipv4"`
	// SuggestedRaftPort is the Raft port this deployment actually publishes.
	// The installer auto-picks a non-default one when 7000 is taken on the
	// host and passes it in via NODE_STATS_RAFT_PORT — the wizard's bind and
	// advertise defaults must follow it or the in-container listener won't
	// match the published host port.
	SuggestedRaftPort string `json:"suggested_raft_port"`
}

// DetectMachineHints probes hostname and primary IPv4 the same way registration does, but never fails the caller.
func DetectMachineHints(ctx context.Context) MachineHints {
	var out MachineHints
	out.SuggestedRaftPort = defaultRaftPort()
	if h := hostnameHint(); h != "" {
		out.SuggestedHostname = h
	} else if hi, err := host.InfoWithContext(ctx); err == nil && hi.Hostname != "" {
		out.SuggestedHostname = hi.Hostname
	}
	// Prefer an explicit NODE_STATS_IPV4 (the installer detects the host's
	// outbound-interface IP on the host and passes it in — inside a bridge
	// network the in-container probe below only sees the container's docker IP).
	if ip := strings.TrimSpace(os.Getenv("NODE_STATS_IPV4")); ip != "" {
		out.SuggestedIPv4 = ip
	} else if ip := hostnet.HostPrimaryIPv4(); ip != "" {
		// Host's default-route IPv4 via HOST_PROC/1/net - the in-container
		// probe below only sees the docker bridge address (172.x).
		out.SuggestedIPv4 = ip
	} else if ip := primaryIPv4Hint(ctx); ip != "" {
		out.SuggestedIPv4 = ip
	}
	return out
}

func hostnameHint() string {
	if hostEtc := strings.TrimSpace(os.Getenv("HOST_ETC")); hostEtc != "" {
		path := filepath.Join(hostEtc, "hostname")
		if data, err := os.ReadFile(path); err == nil {
			if h := strings.TrimSpace(strings.ReplaceAll(string(data), "\n", "")); h != "" {
				return h
			}
		}
	}
	return ""
}

func primaryIPv4Hint(_ context.Context) string {
	if conn, dialErr := net.Dial("udp", "8.8.8.8:80"); dialErr == nil {
		defer conn.Close()
		if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil {
			return udpAddr.IP.String()
		}
	}
	return ""
}
