package network

import (
	"context"
	"net"

	"github.com/charmbracelet/log"
	gopsutilnet "github.com/shirou/gopsutil/v4/net"

	hostnet "system-stats/internal/platform/hostnet"
)

type networkCollector struct {
	logger *log.Logger
}

func newNetworkCollector(logger *log.Logger) *networkCollector {
	return &networkCollector{logger: logger}
}

func (c *networkCollector) Collect(ctx context.Context) (NetworkMetric, error) {
	c.logger.Debug("Collecting network interface statistics")
	netStats, err := gopsutilnet.IOCountersWithContext(ctx, true)
	if err != nil {
		c.logger.Error("Failed to collect network interface statistics", "error", err)
		return NetworkMetric{}, err
	}

	// Addresses / MACs / primary interface change rarely and are served from
	// the shared topology cache (hostnet); only the counters above are read
	// fresh every tick. An interface name we have never seen under the
	// current topology (a new veth, a VPN coming up) refreshes it early.
	topo := hostnet.CurrentTopology(ctx)
	names := make([]string, 0, len(netStats))
	for _, st := range netStats {
		names = append(names, st.Name)
	}
	if topo.NoteNames(names) {
		topo = hostnet.RefreshTopology(ctx)
		topo.NoteNames(names)
	}

	// Docker deployment: take the WHOLE view (counters + addresses + MAC +
	// primary) from the host's network namespace via HOST_PROC/1/net — both
	// gopsutil paths below resolve to the container's netns otherwise.
	hostIfaces, hostDefault, hostNS := topo.HostIfaces, topo.HostDefault, topo.HostNS
	if hostNS {
		if hostStats, herr := hostnet.ParseHostNetDev(); herr == nil && len(hostStats) > 0 {
			netStats = hostStats
		} else {
			// Without host counters, host addresses would mislabel container
			// traffic — fall back to the consistent in-namespace view.
			hostNS = false
		}
	}

	// Native: primary interface by the kernel-picked outbound address.
	primaryIP := ""
	var ifaceDetails gopsutilnet.InterfaceStatList
	if !hostNS {
		primaryIP, ifaceDetails = topo.PrimaryIP, topo.Ifaces
		if topo.HostNS {
			// Host view configured but its counters were unreadable this
			// tick: the cached topology carries no in-namespace details, so
			// resolve them directly for this (rare) fallback pass.
			primaryIP, ifaceDetails = hostnet.NativeView(ctx)
		}
	}

	interfaces := make([]NetworkInterface, 0, len(netStats))
	for _, stat := range netStats {
		// Skip loopback interfaces
		if stat.Name == "lo" || stat.Name == "lo0" {
			continue
		}

		isPrimary := false
		ips := make([]string, 0, 2)
		mac := ""
		if hostNS {
			d := hostIfaces[stat.Name]
			if d == nil {
				continue // host iface without IPv4 (veth*, bridges w/o address)
			}
			ips = append(ips, d.IPs...)
			mac = d.MAC
			isPrimary = stat.Name == hostDefault
		}
		for _, d := range ifaceDetails {
			if d.Name != stat.Name {
				continue
			}
			for _, addr := range d.Addrs {
				// Extract only IPv4 addresses; ignore IPv6 or non-IPv4 entries.
				var ip net.IP
				if parsedIP, _, err := net.ParseCIDR(addr.Addr); err == nil {
					ip = parsedIP
				} else {
					ip = net.ParseIP(addr.Addr)
				}
				if ip == nil || ip.To4() == nil {
					continue
				}
				ipStr := ip.String()
				ips = append(ips, ipStr)
				if primaryIP != "" && ipStr == primaryIP {
					isPrimary = true
				}
			}
			mac = d.HardwareAddr
			// found the interface detail, no need to continue
			break
		}

		// Skip interfaces that do not have any IPv4 address
		if len(ips) == 0 {
			continue
		}

		interfaces = append(interfaces, NetworkInterface{
			Name:        stat.Name,
			IPs:         ips,
			Mac:         mac,
			BytesSent:   stat.BytesSent,
			BytesRecv:   stat.BytesRecv,
			PacketsSent: stat.PacketsSent,
			PacketsRecv: stat.PacketsRecv,
			Errin:       stat.Errin,
			Errout:      stat.Errout,
			Dropin:      stat.Dropin,
			Dropout:     stat.Dropout,
			IsPrimary:   isPrimary,
		})
	}

	c.logger.Debug("Network metrics collected successfully", "interfaces_count", len(interfaces))
	return NetworkMetric{
		Interfaces: interfaces,
	}, nil
}
