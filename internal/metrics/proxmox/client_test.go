package proxmox

import (
	"sort"
	"testing"
)

func TestConfigMACs(t *testing.T) {
	cfg := map[string]any{
		"net0":   "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1",
		"net1":   "e1000=11:22:33:44:55:66,bridge=vmbr1",
		"net2":   "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth", // lxc style
		"ostype": "l26",
		"cores":  float64(4),
		"net9":   "bridge=vmbr0", // no MAC
	}
	macs := ConfigMACs(cfg)
	sort.Strings(macs)
	want := []string{"11:22:33:44:55:66", "aa:bb:cc:dd:ee:ff", "bc:24:11:00:00:01"}
	if len(macs) != len(want) {
		t.Fatalf("got %v, want %v", macs, want)
	}
	for i := range want {
		if macs[i] != want[i] {
			t.Errorf("macs[%d] = %q, want %q", i, macs[i], want[i])
		}
	}
}

func TestConfigIPv4(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "lxc static ip with cidr",
			cfg:  map[string]any{"net0": "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=192.168.1.50/24,gw=192.168.1.1,type=veth"},
			want: "192.168.1.50",
		},
		{
			name: "lxc dhcp has no static ip",
			cfg:  map[string]any{"net0": "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth"},
			want: "",
		},
		{
			name: "qemu net has no ip field",
			cfg:  map[string]any{"net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1"},
			want: "",
		},
		{
			name: "ipv6 only is ignored",
			cfg:  map[string]any{"net0": "name=eth0,bridge=vmbr0,ip6=2001:db8::5/64,type=veth"},
			want: "",
		},
		{
			name: "second nic carries the static ip",
			cfg:  map[string]any{"net0": "name=eth0,ip=dhcp,type=veth", "net1": "name=eth1,ip=10.0.0.7/8,type=veth"},
			want: "10.0.0.7",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ConfigIPv4(c.cfg); got != c.want {
				t.Errorf("ConfigIPv4 = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSMBIOSUUID(t *testing.T) {
	cases := []struct {
		smbios1 any
		want    string
	}{
		{"uuid=11111111-2222-3333-4444-555555555555", "11111111-2222-3333-4444-555555555555"},
		{"uuid=AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE,serial=c2VyaWFs,base64=1", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"serial=abc", ""},
		{nil, ""},
	}
	for _, c := range cases {
		cfg := map[string]any{}
		if c.smbios1 != nil {
			cfg["smbios1"] = c.smbios1
		}
		if got := SMBIOSUUID(cfg); got != c.want {
			t.Errorf("SMBIOSUUID(%v) = %q, want %q", c.smbios1, got, c.want)
		}
	}
}

func TestOSTypeInfo(t *testing.T) {
	cases := []struct {
		ostype                   string
		osName, platform, family string
	}{
		{"l26", "linux", "linux", "linux"},
		{"win11", "windows", "win11", "windows"},
		{"w2k19", "windows", "w2k19", "windows"},
		{"wxp", "windows", "wxp", "windows"},
		{"debian", "linux", "debian", "linux"},
		{"alpine", "linux", "alpine", "linux"},
		{"archlinux", "linux", "archlinux", "linux"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		osName, platform, family := OSTypeInfo(c.ostype)
		if osName != c.osName || platform != c.platform || family != c.family {
			t.Errorf("OSTypeInfo(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.ostype, osName, platform, family, c.osName, c.platform, c.family)
		}
	}
}

func TestFingerprint(t *testing.T) {
	clustered := []ClusterStatusItem{
		{Type: "node", Name: "pve1"},
		{Type: "cluster", Name: "homelab"},
		{Type: "node", Name: "pve2"},
	}
	if fp := Fingerprint(clustered); fp != "cluster/homelab" {
		t.Errorf("clustered fingerprint = %q", fp)
	}
	standalone := []ClusterStatusItem{{Type: "node", Name: "pve1"}}
	if fp := Fingerprint(standalone); fp != "node/pve1" {
		t.Errorf("standalone fingerprint = %q", fp)
	}
	if fp := Fingerprint(nil); fp != "" {
		t.Errorf("empty fingerprint = %q", fp)
	}
}
