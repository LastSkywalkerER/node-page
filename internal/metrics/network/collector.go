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

	// Docker deployment: take the WHOLE view (counters + addresses + MAC +
	// primary) from the host's network namespace via HOST_PROC/1/net — both
	// gopsutil paths below resolve to the container's netns otherwise.
	hostIfaces, hostDefault, hostNS := hostnet.HostNetNS()
	if hostNS {
		if hostStats, herr := hostnet.ParseHostNetDev(); herr == nil && len(hostStats) > 0 {
			netStats = hostStats
		} else {
			// Without host counters, host addresses would mislabel container
			// traffic — fall back to the consistent in-namespace view.
			hostNS = false
		}
	}

	// Determine primary interface by local IP using UDP dial trick
	primaryIP := ""
	if !hostNS {
		if conn, dialErr := net.Dial("udp", "8.8.8.8:80"); dialErr == nil {
			if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil {
				primaryIP = udpAddr.IP.String()
			}
			conn.Close()
		}
	}

	// Also fetch interface address info to map names to IPs
	var ifaceDetails gopsutilnet.InterfaceStatList
	if !hostNS {
		ifaceDetails, _ = gopsutilnet.InterfacesWithContext(ctx)
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
