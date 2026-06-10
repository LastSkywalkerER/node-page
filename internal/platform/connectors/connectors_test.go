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

func TestExternalIDPrefix(t *testing.T) {
	if got := ExternalIDPrefix(TypeProxmox, "cluster/homelab"); got != "proxmox:cluster/homelab/" {
		t.Errorf("prefix = %q", got)
	}
}
