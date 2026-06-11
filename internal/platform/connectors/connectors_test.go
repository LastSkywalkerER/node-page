package connectors

import "testing"

func TestCipherRoundtrip(t *testing.T) {
	c := NewCipher("test-jwt-secret")
	enc, err := c.Encrypt("PVE-token-secret-uuid")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plain, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "PVE-token-secret-uuid" {
		t.Errorf("roundtrip = %q", plain)
	}
	// A different key (changed JWT secret) must fail, not return garbage.
	if _, err := NewCipher("other-secret").Decrypt(enc); err == nil {
		t.Error("decrypt with wrong key succeeded")
	}
	if _, err := c.Decrypt(enc[:4]); err == nil {
		t.Error("decrypt of truncated ciphertext succeeded")
	}
}

func TestVMIDFromMountinfo(t *testing.T) {
	cases := []struct {
		name string
		data string
		want int
	}{
		{"lvm-thin", `65 30 252:5 / / rw,relatime - ext4 /dev/mapper/pve-vm--105--disk--0 rw`, 105},
		{"zfs", `65 30 0:24 / / rw,relatime - zfs rpool/data/subvol-213-disk-0 rw,xattr`, 213},
		{"dir-raw", `65 30 7:1 / / rw,relatime - ext4 /dev/loop1 rw
66 65 0:25 / /mnt rw - ext4 /var/lib/vz/images/451/vm-451-disk-0.raw rw`, 451},
		{"no-pve-volume", `65 30 259:2 / / rw,relatime - ext4 /dev/nvme0n1p2 rw`, 0},
		{"docker-overlay-only", `65 30 0:50 / / rw - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/X`, 0},
	}
	for _, c := range cases {
		if got := vmidFromMountinfo([]byte(c.data)); got != c.want {
			t.Errorf("%s: vmidFromMountinfo = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestExternalIDPrefix(t *testing.T) {
	if got := ExternalIDPrefix(TypeProxmox, "cluster/homelab"); got != "proxmox:cluster/homelab/" {
		t.Errorf("prefix = %q", got)
	}
}
