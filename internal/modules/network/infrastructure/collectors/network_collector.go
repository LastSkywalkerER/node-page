package collectors

import (
	"context"
	"net"

	"github.com/charmbracelet/log"
	gopsutilnet "github.com/shirou/gopsutil/v4/net"

	"system-stats/internal/modules/network/infrastructure/entities"
)

/**
 * networkMetricsCollector implements the NetworkMetricsCollector interface.
 * This collector gathers network performance statistics using cross-platform
 * system monitoring libraries (gopsutil).
 */
type NetworkMetricsCollector struct {
	logger *log.Logger
}

/**
 * NewNetworkMetricsCollector creates a new network metrics collector instance.
 * This constructor initializes the collector for gathering network statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *networkMetricsCollector Returns the initialized network collector
 */
func NewNetworkMetricsCollector(logger *log.Logger) *NetworkMetricsCollector {
	return &NetworkMetricsCollector{logger: logger}
}

/**
 * CollectNetworkMetrics gathers current network performance statistics.
 * This method collects network interface statistics including bytes sent/received
 * and packet counts, excluding loopback interfaces.
 *
 * @param ctx The context for the operation
 * @return entities.NetworkMetric The collected network metrics
 * @return error Returns an error if network metrics collection fails
 */
func (c *NetworkMetricsCollector) CollectNetworkMetrics(ctx context.Context) (entities.NetworkMetric, error) {
	c.logger.Info("Collecting network interface statistics")
	netStats, err := gopsutilnet.IOCountersWithContext(ctx, true)
	if err != nil {
		c.logger.Error("Failed to collect network interface statistics", "error", err)
		return entities.NetworkMetric{}, err
	}

	// Determine primary interface by local IP using UDP dial trick
	primaryIP := ""
	if conn, dialErr := net.Dial("udp", "8.8.8.8:80"); dialErr == nil {
		if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil {
			primaryIP = udpAddr.IP.String()
		}
		conn.Close()
	}

	// Also fetch interface address info to map names to IPs
	ifaceDetails, _ := gopsutilnet.InterfacesWithContext(ctx)

	interfaces := make([]entities.NetworkInterface, 0, len(netStats))
	for _, stat := range netStats {
		// Skip loopback interfaces
		if stat.Name == "lo" || stat.Name == "lo0" {
			continue
		}

		isPrimary := false
		ips := make([]string, 0, 2)
		mac := ""
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

		interfaces = append(interfaces, entities.NetworkInterface{
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

	c.logger.Info("Network metrics collected successfully", "interfaces_count", len(interfaces))
	return entities.NetworkMetric{
		Interfaces: interfaces,
	}, nil
}
