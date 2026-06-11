package network

// Reading metrics through host bind-mounts (Docker deployment) is tricky for
// networking: /proc/net is a SYMLINK to /proc/self/net, so any read of
// <HOST_PROC>/net/* resolves to the network namespace of the READER — the
// container — no matter whose procfs is mounted. The host's view lives under
// a concrete pid instead: <HOST_PROC>/1/net/* is host PID 1's netns (the
// host's), readable without extra capabilities. When HOST_PROC is set, take
// everything from there:
//
//   - HOST_PROC/1/net/dev       per-interface counters (host traffic)
//   - HOST_PROC/1/net/route     per-interface IPv4 prefixes + default route
//   - HOST_PROC/1/net/fib_trie  the host's local (/32 LOCAL) IPv4 addresses
//   - HOST_SYS/class/net/<i>/address  MAC (sysfs binds to the mounter's
//     netns, i.e. the host's)

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gopsutilnet "github.com/shirou/gopsutil/v4/net"
)

type hostIface struct {
	ips []string
	mac string
}

type hostRoute struct {
	iface string
	ipnet *net.IPNet
	ones  int
}

// hostPid1Net returns <HOST_PROC>/1/net, or "" when metrics are not read
// through a host mount (native install — the in-namespace view is already
// the host's).
func hostPid1Net() string {
	hostProc := strings.TrimSpace(os.Getenv("HOST_PROC"))
	if hostProc == "" || hostProc == "/proc" {
		return ""
	}
	return filepath.Join(hostProc, "1", "net")
}

// hostNetNS returns per-interface details and the default-route interface of
// the HOST network namespace. ok is false when HOST_PROC is unset or host
// PID 1's net view can't be parsed.
func hostNetNS() (details map[string]*hostIface, defaultIface string, ok bool) {
	netDir := hostPid1Net()
	if netDir == "" {
		return nil, "", false
	}
	routes, defIface, err := parseHostRoutes(filepath.Join(netDir, "route"))
	if err != nil || (len(routes) == 0 && defIface == "") {
		return nil, "", false
	}

	details = make(map[string]*hostIface)
	for _, ip := range parseLocalIPv4s(filepath.Join(netDir, "fib_trie")) {
		iface := matchRouteIface(routes, ip)
		if iface == "" {
			// Point-to-point setups (common on VPSes) carry a /32 address
			// covered by no on-link prefix — it belongs to the default iface.
			iface = defIface
		}
		if iface == "" {
			continue
		}
		d := details[iface]
		if d == nil {
			d = &hostIface{}
			details[iface] = d
		}
		d.ips = append(d.ips, ip.String())
	}

	hostSys := strings.TrimSpace(os.Getenv("HOST_SYS"))
	if hostSys == "" {
		hostSys = "/sys"
	}
	for name, d := range details {
		if b, err := os.ReadFile(filepath.Join(hostSys, "class", "net", name, "address")); err == nil {
			d.mac = strings.TrimSpace(string(b))
		}
	}
	return details, defIface, true
}

// parseHostNetDev reads host PID 1's /proc/net/dev — gopsutil's IOCounters
// resolve HOST_PROC/net/dev through the /proc/net → self/net symlink and
// therefore return the container's counters, so the host's must be read
// from the pid-qualified path ourselves.
func parseHostNetDev() ([]gopsutilnet.IOCountersStat, error) {
	netDir := hostPid1Net()
	if netDir == "" {
		return nil, errors.New("not a host-mounted /proc")
	}
	data, err := os.ReadFile(filepath.Join(netDir, "dev"))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]gopsutilnet.IOCountersStat, 0, len(lines))
	for i, ln := range lines {
		if i < 2 { // two header lines
			continue
		}
		name, rest, found := strings.Cut(ln, ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 16 {
			continue
		}
		u := func(idx int) uint64 {
			v, _ := strconv.ParseUint(f[idx], 10, 64)
			return v
		}
		out = append(out, gopsutilnet.IOCountersStat{
			Name:        strings.TrimSpace(name),
			BytesRecv:   u(0),
			PacketsRecv: u(1),
			Errin:       u(2),
			Dropin:      u(3),
			BytesSent:   u(8),
			PacketsSent: u(9),
			Errout:      u(10),
			Dropout:     u(11),
		})
	}
	return out, nil
}

// parseHostRoutes reads a pid-qualified /proc/<pid>/net/route (hex
// little-endian IPv4 columns). Returns the up on-link prefixes and the
// default route's interface.
func parseHostRoutes(path string) ([]hostRoute, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var routes []hostRoute
	defIface := ""
	lines := strings.Split(string(data), "\n")
	for i, ln := range lines {
		if i == 0 {
			continue // header
		}
		f := strings.Fields(ln)
		if len(f) < 8 {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x1 == 0 { // RTF_UP
			continue
		}
		dst := hexLEToIPv4(f[1])
		mask := hexLEToIPv4(f[7])
		if dst == nil || mask == nil {
			continue
		}
		if dst.Equal(net.IPv4zero) && mask.Equal(net.IPv4zero) {
			if defIface == "" {
				defIface = f[0]
			}
			continue
		}
		m := net.IPMask(mask.To4())
		ones, _ := m.Size()
		routes = append(routes, hostRoute{
			iface: f[0],
			ipnet: &net.IPNet{IP: dst.Mask(m), Mask: m},
			ones:  ones,
		})
	}
	return routes, defIface, nil
}

// hexLEToIPv4 parses /proc/net/route's little-endian hex IPv4 (0101A8C0 →
// 192.168.1.1).
func hexLEToIPv4(s string) net.IP {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil
	}
	return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).To4()
}

// parseLocalIPv4s extracts the host's own addresses from /proc/net/fib_trie:
// a leaf line ("|-- A.B.C.D") whose attribute line below says "/32 host
// LOCAL". Loopback is skipped; duplicates (Main + Local tries) collapse.
func parseLocalIPv4s(path string) []net.IP {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []net.IP
	var leaf net.IP
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "|--"):
			leaf = nil
			if ip := net.ParseIP(strings.TrimSpace(strings.TrimPrefix(t, "|--"))); ip != nil {
				leaf = ip.To4()
			}
		case strings.HasPrefix(t, "+--"):
			leaf = nil
		case leaf != nil && strings.HasPrefix(t, "/32 host LOCAL"):
			if !leaf.IsLoopback() && !seen[leaf.String()] {
				seen[leaf.String()] = true
				out = append(out, leaf)
			}
		}
	}
	return out
}

// matchRouteIface picks the interface of the longest prefix containing ip.
func matchRouteIface(routes []hostRoute, ip net.IP) string {
	best, bestOnes := "", -1
	for _, r := range routes {
		if r.ipnet.Contains(ip) && r.ones > bestOnes {
			best, bestOnes = r.iface, r.ones
		}
	}
	return best
}
