package collectors

import (
	"context"
	"net"
	"sort"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/host"
	gopsutilnet "github.com/shirou/gopsutil/v4/net"

	"system-stats/internal/modules/hosts/infrastructure/entities"
)

/**
 * HostCollector implements the HostCollector interface.
 * This collector gathers host information including hostname and MAC address.
 */
type HostCollector struct {
	logger *log.Logger
}

/**
 * NewHostCollector creates a new host collector instance.
 * This constructor initializes the collector for gathering host information.
 *
 * @param logger The logger instance for logging collection operations
 * @return *HostCollector Returns the initialized host collector
 */
func NewHostCollector(logger *log.Logger) *HostCollector {
	return &HostCollector{logger: logger}
}

/**
 * CollectHostInfo gathers current host information including hostname and MAC address.
 * This method collects host info using cross-platform system monitoring libraries (gopsutil).
 *
 * @param ctx The context for the operation
 * @return entities.HostInfo The collected host information
 * @return error Returns an error if host info collection fails
 */
func (c *HostCollector) CollectHostInfo(ctx context.Context) (entities.HostInfo, error) {
	c.logger.Info("Collecting host information")

	// Get hostname and system info
	hostInfo, err := host.InfoWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect hostname", "error", err)
		return entities.HostInfo{}, err
	}

	hostname := hostInfo.Hostname

	// Determine primary local IP via UDP dial trick
	// This does not actually send traffic but lets kernel pick the outbound interface
	primaryIP := ""
	if conn, dialErr := net.Dial("udp", "8.8.8.8:80"); dialErr == nil {
		if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil {
			primaryIP = udpAddr.IP.String()
		}
		conn.Close()
	}

	// Get network interfaces to find MAC address; prefer interface matching primaryIP
	interfaces, err := gopsutilnet.InterfacesWithContext(ctx)
	if err != nil {
		c.logger.Error("Failed to collect network interfaces", "error", err)
		return entities.HostInfo{}, err
	}

	var macAddress string
	var ipv4 string
	// First pass: try to match by primary IPv4
	if primaryIP != "" {
		for _, iface := range interfaces {
			if iface.HardwareAddr == "" || iface.Name == "lo" || iface.Name == "lo0" {
				continue
			}
			if _, err := net.ParseMAC(iface.HardwareAddr); err != nil {
				continue
			}
			for _, addr := range iface.Addrs {
				// Consider only IPv4 addresses; addr.Addr may be with CIDR
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
				if ipStr == primaryIP {
					macAddress = iface.HardwareAddr
					ipv4 = ipStr
					c.logger.Info("Selected primary interface by IPv4", "interface", iface.Name, "ip", ipStr, "mac", macAddress)
					break
				}
			}
			if macAddress != "" {
				break
			}
		}
	}
	// Second pass: if primary interface has no MAC, prefer interfaces with highest received bytes (non-loopback)
	if macAddress == "" {
		if ioCounters, err := gopsutilnet.IOCountersWithContext(ctx, true); err == nil {
			recvByName := make(map[string]uint64, len(ioCounters))
			for _, c := range ioCounters {
				recvByName[c.Name] = c.BytesRecv
			}

			type ifaceScore struct {
				idx int
				rx  uint64
			}

			var scores []ifaceScore
			for idx, iface := range interfaces {
				if iface.Name == "lo" || iface.Name == "lo0" {
					continue
				}
				rx := recvByName[iface.Name]
				if rx == 0 {
					continue
				}
				scores = append(scores, ifaceScore{idx: idx, rx: rx})
			}

			sort.Slice(scores, func(i, j int) bool { return scores[i].rx > scores[j].rx })
			for _, s := range scores {
				iface := interfaces[s.idx]
				if iface.HardwareAddr == "" {
					continue
				}
				if _, err := net.ParseMAC(iface.HardwareAddr); err != nil {
					continue
				}
				// Require the interface to have at least one IPv4 address
				hasIPv4 := false
				for _, addr := range iface.Addrs {
					var ip net.IP
					if parsedIP, _, err := net.ParseCIDR(addr.Addr); err == nil {
						ip = parsedIP
					} else {
						ip = net.ParseIP(addr.Addr)
					}
					if ip != nil && ip.To4() != nil {
						hasIPv4 = true
						if ipv4 == "" {
							ipv4 = ip.String()
						}
						break
					}
				}
				if !hasIPv4 {
					continue
				}
				macAddress = iface.HardwareAddr
				c.logger.Info("Selected interface by received bytes (IPv4)", "interface", iface.Name, "rx_bytes", recvByName[iface.Name], "mac", macAddress)
				break
			}
		}

		// Final fallback: first valid non-loopback MAC if nothing else matched
		if macAddress == "" {
			for _, iface := range interfaces {
				if iface.HardwareAddr == "" || iface.Name == "lo" || iface.Name == "lo0" {
					continue
				}
				if _, err := net.ParseMAC(iface.HardwareAddr); err != nil {
					continue
				}
				// Require at least one IPv4 address on the interface
				hasIPv4 := false
				for _, addr := range iface.Addrs {
					var ip net.IP
					if parsedIP, _, err := net.ParseCIDR(addr.Addr); err == nil {
						ip = parsedIP
					} else {
						ip = net.ParseIP(addr.Addr)
					}
					if ip != nil && ip.To4() != nil {
						hasIPv4 = true
						if ipv4 == "" {
							ipv4 = ip.String()
						}
						break
					}
				}
				if !hasIPv4 {
					continue
				}
				macAddress = iface.HardwareAddr
				c.logger.Info("Fallback to first valid MAC address (IPv4)", "interface", iface.Name, "mac", macAddress)
				break
			}
		}
	}

	if macAddress == "" {
		c.logger.Error("No valid MAC address found")
		return entities.HostInfo{}, net.InvalidAddrError("no valid MAC address found")
	}

	c.logger.Info("Host information collected successfully", "hostname", hostname, "mac_address", macAddress)
	return entities.HostInfo{
		Name:                 hostname,
		MacAddress:           macAddress,
		IPv4:                 ipv4,
		OS:                   hostInfo.OS,
		Platform:             hostInfo.Platform,
		PlatformFamily:       hostInfo.PlatformFamily,
		PlatformVersion:      hostInfo.PlatformVersion,
		KernelVersion:        hostInfo.KernelVersion,
		VirtualizationSystem: hostInfo.VirtualizationSystem,
		VirtualizationRole:   hostInfo.VirtualizationRole,
		HostID:               hostInfo.HostID,
	}, nil
}
