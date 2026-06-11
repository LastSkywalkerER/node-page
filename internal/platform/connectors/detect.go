package connectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

// detectCacheTTL bounds how often the (cheap) probes re-run. The environment
// a machine is virtualized in does not change at runtime, but HOST_* mounts
// may appear after a controller-driven recreate, so we re-check occasionally.
const detectCacheTTL = 5 * time.Minute

// Detector runs credential-free local probes that hint at the machine being a
// Proxmox guest. All probes are best-effort, never fail hard, and honour the
// HOST_PROC / HOST_SYS env redirects used by the Docker deployment.
type Detector struct {
	logger *log.Logger

	mu        sync.Mutex
	cached    []DiscoveredHint
	checkedAt time.Time
}

// NewDetector creates a Detector.
func NewDetector(logger *log.Logger) *Detector {
	return &Detector{logger: logger}
}

// Detect returns the current set of discovered hints (0 or 1 entries today).
func (d *Detector) Detect() []DiscoveredHint {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.checkedAt.IsZero() && time.Since(d.checkedAt) < detectCacheTTL {
		return d.cached
	}
	d.cached = d.probeProxmox()
	d.checkedAt = time.Now()
	return d.cached
}

func hostProc() string {
	if v := strings.TrimSpace(os.Getenv("HOST_PROC")); v != "" {
		return v
	}
	return "/proc"
}

func hostSys() string {
	if v := strings.TrimSpace(os.Getenv("HOST_SYS")); v != "" {
		return v
	}
	return "/sys"
}

var lxcCgroupRe = regexp.MustCompile(`(?m)[:/]lxc/(\d+)`)

// pveVolumeRe matches the PVE storage volume name of an LXC rootfs as it leaks
// through /proc/1/mountinfo: vm--105--disk--0 (LVM-thin), subvol-105-disk-0
// (ZFS), /images/105/ (dir storage). Unlike the cgroup path, the mount source
// survives the container's cgroup/mount namespaces.
var pveVolumeRe = regexp.MustCompile(`(?:vm|subvol)-+(\d+)-+disk|/images/(\d+)/`)

// vmidFromMountinfo extracts this container's VMID from its mountinfo.
func vmidFromMountinfo(data []byte) int {
	if m := pveVolumeRe.FindSubmatch(data); m != nil {
		for _, g := range m[1:] {
			if len(g) > 0 {
				v, _ := strconv.Atoi(string(g))
				return v
			}
		}
	}
	return 0
}

func (d *Detector) probeProxmox() []DiscoveredHint {
	var (
		evidence  []string
		guestKind string
		vmid      int
		high      bool
	)

	proc := hostProc()
	sys := hostSys()

	// LXC: PID 1 of the (host's) pid namespace carries container=lxc, and
	// Proxmox puts containers into an "lxc/<vmid>" cgroup — which also leaks
	// this machine's VMID.
	if env, err := os.ReadFile(filepath.Join(proc, "1", "environ")); err == nil &&
		bytes.Contains(env, []byte("container=lxc")) {
		guestKind = "lxc"
		evidence = append(evidence, "proc:container=lxc")
	}
	if cg, err := os.ReadFile(filepath.Join(proc, "1", "cgroup")); err == nil {
		if m := lxcCgroupRe.FindSubmatch(cg); m != nil {
			guestKind = "lxc"
			high = true
			vmid, _ = strconv.Atoi(string(m[1]))
			evidence = append(evidence, fmt.Sprintf("cgroup:/lxc/%d", vmid))
		}
	}
	// Inside a real PVE LXC the cgroup namespace hides the /lxc/<vmid> path
	// (PID 1 sees 0::/init.scope), but the rootfs volume name in mountinfo
	// still carries the VMID. Only trusted once an LXC marker was seen — a
	// PVE node with a guest volume mounted must not claim to be that guest.
	if vmid == 0 && guestKind == "lxc" {
		if mi, err := os.ReadFile(filepath.Join(proc, "1", "mountinfo")); err == nil {
			if v := vmidFromMountinfo(mi); v > 0 {
				vmid = v
				high = true
				evidence = append(evidence, fmt.Sprintf("mountinfo:disk-of-vmid-%d", v))
			}
		}
	}

	// QEMU/KVM VM: DMI vendor + the canonical QEMU machine-type product name.
	if guestKind == "" {
		vendor := readTrimmed(filepath.Join(sys, "class", "dmi", "id", "sys_vendor"))
		if strings.EqualFold(vendor, "QEMU") {
			guestKind = "vm"
			evidence = append(evidence, "dmi:sys_vendor=QEMU")
			if product := readTrimmed(filepath.Join(sys, "class", "dmi", "id", "product_name")); strings.Contains(product, "Standard PC") {
				evidence = append(evidence, "dmi:product_name="+product)
			}
		}
		// QEMU guest-agent virtio channel — present when the VM was created
		// with the agent enabled (the Proxmox default recommendation).
		if _, err := os.Stat("/dev/virtio-ports/org.qemu.guest_agent.0"); err == nil {
			if guestKind == "" {
				guestKind = "vm"
			}
			high = true
			evidence = append(evidence, "dev:org.qemu.guest_agent.0")
		}
	}

	if guestKind == "" {
		return nil
	}

	confidence := "medium"
	if high {
		confidence = "high"
	}

	hint := DiscoveredHint{
		Type:       TypeProxmox,
		Confidence: confidence,
		GuestKind:  guestKind,
		VMIDHint:   vmid,
		Evidence:   evidence,
	}
	if gw := defaultGateway(proc); gw != "" {
		hint.SuggestedEndpoint = "https://" + gw + ":8006"
	}
	if d.logger != nil {
		d.logger.Debug("connectors: proxmox environment hint",
			"kind", guestKind, "confidence", confidence, "evidence", strings.Join(evidence, ","))
	}
	return []DiscoveredHint{hint}
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// defaultGateway parses /proc/net/route for the 0.0.0.0/0 entry. The gateway
// of a Proxmox guest is very often the PVE node itself (vmbr0), which makes a
// good pre-fill for the connector endpoint.
func defaultGateway(proc string) string {
	data, err := os.ReadFile(filepath.Join(proc, "net", "route"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		raw, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil || raw == 0 {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, uint32(raw))
		return ip.String()
	}
	return ""
}
