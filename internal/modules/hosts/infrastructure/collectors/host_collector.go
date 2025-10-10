package collectors

import (
	"context"
	"net"

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

	// Get hostname
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
	// First pass: try to match by primary IP
	if primaryIP != "" {
		for _, iface := range interfaces {
			if iface.HardwareAddr == "" || iface.Name == "lo" || iface.Name == "lo0" {
				continue
			}
			if _, err := net.ParseMAC(iface.HardwareAddr); err != nil {
				continue
			}
			for _, addr := range iface.Addrs {
				if addr.Addr == primaryIP || addr.Addr == primaryIP+"/32" { // gopsutil may include CIDR
					macAddress = iface.HardwareAddr
					c.logger.Info("Selected primary interface by IP", "interface", iface.Name, "ip", addr.Addr, "mac", macAddress)
					break
				}
			}
			if macAddress != "" {
				break
			}
		}
	}
	// Second pass: fallback to first valid non-loopback MAC
	if macAddress == "" {
		for _, iface := range interfaces {
			if iface.HardwareAddr == "" || iface.Name == "lo" || iface.Name == "lo0" {
				continue
			}
			if _, err := net.ParseMAC(iface.HardwareAddr); err != nil {
				continue
			}
			macAddress = iface.HardwareAddr
			c.logger.Info("Fallback to first valid MAC address", "interface", iface.Name, "mac", macAddress)
			break
		}
	}

	if macAddress == "" {
		c.logger.Error("No valid MAC address found")
		return entities.HostInfo{}, net.InvalidAddrError("no valid MAC address found")
	}

	c.logger.Info("Host information collected successfully", "hostname", hostname, "mac_address", macAddress)
	return entities.HostInfo{
		Name:       hostname,
		MacAddress: macAddress,
	}, nil
}
