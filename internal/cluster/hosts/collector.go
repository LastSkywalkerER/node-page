package hosts

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v4/host"
	gopsutilnet "github.com/shirou/gopsutil/v4/net"

	hostnet "system-stats/internal/platform/hostnet"
)

func init() {
	// gopsutil re-derives the boot time from /proc/stat (scanning past the
	// multi-KB intr line) on every call unless told to cache it. It is
	// constant for the life of the process.
	host.EnableBootTimeCache(true)
}

// staticInfoRefresh bounds how often the OS identity (platform, kernel,
// virtualization, host id) is re-read. gopsutil's host.Info walks /etc/*-release,
// may fork lsb_release, and lists ALL of /proc to count processes — once an
// hour is plenty for values that only move on an OS upgrade.
const staticInfoRefresh = time.Hour

// HostCollector implements the HostCollector interface.
// This collector gathers host information including hostname and MAC address.
type HostCollector struct {
	logger *log.Logger

	staticMu    sync.Mutex
	staticInfo  *host.InfoStat
	staticAt    time.Time
	productUUID string
}

// newHostCollector creates a new host collector instance.
// This constructor initializes the collector for gathering host information.
func newHostCollector(logger *log.Logger) *HostCollector {
	return &HostCollector{logger: logger}
}

// osIdentity returns the (cached) static OS identity plus the SMBIOS UUID.
func (c *HostCollector) osIdentity(ctx context.Context) (*host.InfoStat, string, error) {
	c.staticMu.Lock()
	defer c.staticMu.Unlock()
	if c.staticInfo != nil && time.Since(c.staticAt) < staticInfoRefresh {
		return c.staticInfo, c.productUUID, nil
	}
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		if c.staticInfo != nil {
			return c.staticInfo, c.productUUID, nil // serve the previous value
		}
		return nil, "", err
	}
	c.staticInfo = info
	c.staticAt = time.Now()
	c.productUUID = readProductUUID()
	return info, c.productUUID, nil
}

// CollectHostInfo gathers current host information including hostname and MAC address.
// This method collects host info using cross-platform system monitoring libraries (gopsutil).
func (c *HostCollector) CollectHostInfo(ctx context.Context) (HostInfo, error) {
	c.logger.Debug("Collecting host information")

	// Static OS identity (cached); hostname is re-read each time — it is
	// cheap and an operator may rename the machine.
	hostInfo, productUUID, err := c.osIdentity(ctx)
	if err != nil {
		c.logger.Error("Failed to collect host information", "error", err)
		return HostInfo{}, err
	}

	hostname := hostInfo.Hostname
	if h, herr := os.Hostname(); herr == nil && h != "" {
		hostname = h
	}
	if override := strings.TrimSpace(os.Getenv("NODE_STATS_HOSTNAME")); override != "" {
		hostname = override
		c.logger.Debug("Hostname from NODE_STATS_HOSTNAME", "hostname", hostname)
	} else if hostEtc := strings.TrimSpace(os.Getenv("HOST_ETC")); hostEtc != "" {
		path := filepath.Join(hostEtc, "hostname")
		if data, err := os.ReadFile(path); err == nil {
			if h := strings.TrimSpace(strings.ReplaceAll(string(data), "\n", "")); h != "" {
				hostname = h
				c.logger.Debug("Hostname from HOST_ETC", "path", path, "hostname", hostname)
			}
		}
	}

	// Primary local IP + interface list come from the shared topology cache
	// (hostnet): the UDP "dial" (kernel picks the outbound interface, no
	// packet is sent) and the per-interface netlink address dumps are
	// refreshed on its slow cadence, not on every registration.
	topo := hostnet.CurrentTopology(ctx)
	primaryIP := topo.PrimaryIP
	interfaces := topo.Ifaces
	if topo.HostNS {
		// Host-mounted view: the topology carries the host's addresses, but
		// the MAC that identifies this row must stay container-based (see
		// below), so list the reader's own interfaces here.
		var ierr error
		interfaces, ierr = gopsutilnet.InterfacesWithContext(ctx)
		if ierr != nil {
			c.logger.Error("Failed to collect network interfaces", "error", ierr)
			return HostInfo{}, ierr
		}
	}
	if len(interfaces) == 0 {
		var ierr error
		interfaces, ierr = gopsutilnet.InterfacesWithContext(ctx)
		if ierr != nil {
			c.logger.Error("Failed to collect network interfaces", "error", ierr)
			return HostInfo{}, ierr
		}
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
					c.logger.Debug("Selected primary interface by IPv4", "interface", iface.Name, "ip", ipStr, "mac", macAddress)
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
				c.logger.Debug("Selected interface by received bytes (IPv4)", "interface", iface.Name, "rx_bytes", recvByName[iface.Name], "mac", macAddress)
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
				c.logger.Debug("Fallback to first valid MAC address (IPv4)", "interface", iface.Name, "mac", macAddress)
				break
			}
		}
	}

	if macAddress == "" {
		c.logger.Error("No valid MAC address found")
		return HostInfo{}, net.InvalidAddrError("no valid MAC address found")
	}

	// The interface walk above ran in the CONTAINER's netns — its IPv4 is the
	// docker bridge address (172.x), useless for reaching this machine. When
	// the host's /proc is mounted, prefer the host's default-route IPv4.
	// (MAC selection above intentionally stays container-based: it is this
	// row's cluster identity and must not change on upgrades.)
	if hostIP := hostnet.HostPrimaryIPv4(); hostIP != "" {
		ipv4 = hostIP
	}

	if v := strings.TrimSpace(os.Getenv("NODE_STATS_IPV4")); v != "" {
		if ip := net.ParseIP(v); ip != nil && ip.To4() != nil {
			ipv4 = v
			c.logger.Debug("IPv4 from NODE_STATS_IPV4", "ipv4", ipv4)
		}
	}

	bootTime := hostInfo.BootTime
	if bt, berr := host.BootTimeWithContext(ctx); berr == nil && bt > 0 {
		bootTime = bt
	}

	c.logger.Debug("Host information collected successfully", "hostname", hostname, "mac_address", macAddress)
	return HostInfo{
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
		HardwareUUID:         productUUID,
		BootTime:             int64(bootTime),
	}, nil
}

// readProductUUID reads the SMBIOS product UUID. Inside a QEMU VM it equals
// the guest's `smbios1` UUID in its Proxmox config — the linking key for VMs
// whose registered MAC the hypervisor has never seen. Best-effort: the file
// is root-only on most distros; honours HOST_SYS for Docker deployments.
// Note: inside an LXC container sysfs shows the HOST's DMI, but LXC guests
// have no smbios1 config so the value never participates in matching there.
func readProductUUID() string {
	sys := strings.TrimSpace(os.Getenv("HOST_SYS"))
	if sys == "" {
		sys = "/sys"
	}
	data, err := os.ReadFile(filepath.Join(sys, "class", "dmi", "id", "product_uuid"))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(data)))
}
