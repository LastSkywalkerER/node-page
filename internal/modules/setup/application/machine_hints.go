package application

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v4/host"
)

// MachineHints are best-effort values for the setup wizard (no MAC / DB required).
type MachineHints struct {
	SuggestedHostname string `json:"suggested_hostname"`
	SuggestedIPv4     string `json:"suggested_ipv4"`
}

// DetectMachineHints probes hostname and primary IPv4 the same way registration does, but never fails the caller.
func DetectMachineHints(ctx context.Context) MachineHints {
	var out MachineHints
	if h := hostnameHint(); h != "" {
		out.SuggestedHostname = h
	} else if hi, err := host.InfoWithContext(ctx); err == nil && hi.Hostname != "" {
		out.SuggestedHostname = hi.Hostname
	}
	if ip := primaryIPv4Hint(ctx); ip != "" {
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
