package hostnet

import (
	"os"
	"path/filepath"
	"testing"
)

func writeHostNetFixtures(t *testing.T, route, fibTrie string, macs map[string]string) {
	t.Helper()
	dir := t.TempDir()
	// /proc/net is a symlink to /proc/self/net (the reader's netns) — the
	// host view must be read through a concrete pid, host PID 1.
	procNet := filepath.Join(dir, "proc", "1", "net")
	if err := os.MkdirAll(procNet, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procNet, "route"), []byte(route), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procNet, "fib_trie"), []byte(fibTrie), 0o644); err != nil {
		t.Fatal(err)
	}
	for iface, mac := range macs {
		d := filepath.Join(dir, "sys", "class", "net", iface)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "address"), []byte(mac+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOST_PROC", filepath.Join(dir, "proc"))
	t.Setenv("HOST_SYS", filepath.Join(dir, "sys"))
}

// Typical LAN host: eth0 192.168.1.10/24 (default route) + docker0 172.17.0.1/16.
func TestHostNetNSLanHost(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	fib := `Main:
  +-- 0.0.0.0/0 3 0 5
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 172.17.0.0/16 2 0 2
        |-- 172.17.0.1
           /32 host LOCAL
     +-- 192.168.1.0/24 2 0 2
        |-- 192.168.1.10
           /32 host LOCAL
Local:
  +-- 127.0.0.0/8 2 0 2
     |-- 127.0.0.1
        /32 host LOCAL
     |-- 192.168.1.10
        /32 host LOCAL
`
	writeHostNetFixtures(t, route, fib, map[string]string{
		"eth0":    "aa:bb:cc:dd:ee:ff",
		"docker0": "02:42:11:22:33:44",
	})

	details, def, ok := HostNetNS()
	if !ok {
		t.Fatal("HostNetNS not ok")
	}
	if def != "eth0" {
		t.Fatalf("default iface = %q, want eth0", def)
	}
	eth := details["eth0"]
	if eth == nil || len(eth.IPs) != 1 || eth.IPs[0] != "192.168.1.10" {
		t.Fatalf("eth0 = %+v, want 192.168.1.10", eth)
	}
	if eth.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("eth0 mac = %q", eth.MAC)
	}
	d0 := details["docker0"]
	if d0 == nil || len(d0.IPs) != 1 || d0.IPs[0] != "172.17.0.1" {
		t.Fatalf("docker0 = %+v, want 172.17.0.1", d0)
	}
	if _, hasLo := details["lo"]; hasLo {
		t.Fatal("loopback must be skipped")
	}
}

// Hetzner-style VPS: the public address is a /32 covered by no on-link
// prefix (default via a /32 gateway route) — it must fall back to the
// default-route interface instead of being dropped.
func TestHostNetNSPointToPointVPS(t *testing.T) {
	// 203.0.113.83 LE-hex = 5398.15.41 → bytes 41 15 98 53 → "53981541";
	// gateway 172.31.1.1 → "0101 1F AC" → "01011FAC".
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t01011FAC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t01011FAC\t00000000\t0005\t0\t0\t0\tFFFFFFFF\t0\t0\t0\n"
	fib := `Main:
  +-- 0.0.0.0/0 2 0 2
     |-- 203.0.113.83
        /32 host LOCAL
Local:
  +-- 127.0.0.0/8 2 0 2
     |-- 127.0.0.1
        /32 host LOCAL
`
	writeHostNetFixtures(t, route, fib, map[string]string{"eth0": "96:00:02:aa:bb:cc"})

	details, def, ok := HostNetNS()
	if !ok {
		t.Fatal("HostNetNS not ok")
	}
	if def != "eth0" {
		t.Fatalf("default iface = %q, want eth0", def)
	}
	eth := details["eth0"]
	if eth == nil || len(eth.IPs) != 1 || eth.IPs[0] != "203.0.113.83" {
		t.Fatalf("eth0 = %+v, want 203.0.113.83", eth)
	}
	if eth.MAC != "96:00:02:aa:bb:cc" {
		t.Fatalf("eth0 mac = %q", eth.MAC)
	}
}

// Host counters come from the pid-qualified net/dev (gopsutil would resolve
// HOST_PROC/net/dev through the self/net symlink to the container's view).
func TestParseHostNetDev(t *testing.T) {
	dir := t.TempDir()
	procNet := filepath.Join(dir, "proc", "1", "net")
	if err := os.MkdirAll(procNet, 0o755); err != nil {
		t.Fatal(err)
	}
	dev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  111111     222    0    0    0     0          0         0   111111     222    0    0    0     0       0          0
  eth0: 2701325   10124    1    2    0     0          0         0 29671388    9345    3    4    0     0       0          0
`
	if err := os.WriteFile(filepath.Join(procNet, "dev"), []byte(dev), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST_PROC", filepath.Join(dir, "proc"))

	stats, err := ParseHostNetDev()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d stats, want 2", len(stats))
	}
	eth := stats[1]
	if eth.Name != "eth0" {
		t.Fatalf("name = %q", eth.Name)
	}
	if eth.BytesRecv != 2701325 || eth.PacketsRecv != 10124 || eth.Errin != 1 || eth.Dropin != 2 {
		t.Fatalf("rx = %+v", eth)
	}
	if eth.BytesSent != 29671388 || eth.PacketsSent != 9345 || eth.Errout != 3 || eth.Dropout != 4 {
		t.Fatalf("tx = %+v", eth)
	}
}

// Native install (no HOST_PROC) keeps the stdlib in-namespace path.
func TestHostNetNSDisabledNatively(t *testing.T) {
	t.Setenv("HOST_PROC", "")
	if _, _, ok := HostNetNS(); ok {
		t.Fatal("HostNetNS must be disabled when HOST_PROC is unset")
	}
	t.Setenv("HOST_PROC", "/proc")
	if _, _, ok := HostNetNS(); ok {
		t.Fatal("HostNetNS must be disabled when HOST_PROC is /proc itself")
	}
}
