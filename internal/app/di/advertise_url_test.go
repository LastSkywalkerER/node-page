package di

import "testing"

func TestDeriveAdvertiseURLFromAddr(t *testing.T) {
	t.Setenv("ADDR", ":9090")
	if got := deriveAdvertiseURLFromAddr("192.168.0.111:7000"); got != "http://192.168.0.111:9090" {
		t.Fatalf("got %q, want http://192.168.0.111:9090", got)
	}

	// Default HTTP port when ADDR is unset.
	t.Setenv("ADDR", "")
	if got := deriveAdvertiseURLFromAddr("10.0.0.5:7000"); got != "http://10.0.0.5:8080" {
		t.Fatalf("got %q, want http://10.0.0.5:8080", got)
	}

	// No host in the advertise address → empty (caller skips advertising).
	if got := deriveAdvertiseURLFromAddr(":7000"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := deriveAdvertiseURLFromAddr(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
