package bridge

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	secret := "super-secret"
	body := []byte(`{"hello":"world"}`)
	ts := time.Now().UnixNano()
	sig := Sign(secret, ts, body)
	if err := Verify(secret, sig, ts, body); err != nil {
		t.Fatalf("expected verify success, got %v", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	t.Parallel()
	ts := time.Now().UnixNano()
	body := []byte("payload")
	sig := Sign("secretA", ts, body)
	if err := Verify("secretB", sig, ts, body); err == nil {
		t.Fatal("verify should fail when secrets differ")
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	t.Parallel()
	secret := "abc"
	ts := time.Now().UnixNano()
	body := []byte("payload")
	sig := Sign(secret, ts, body)
	if err := Verify(secret, sig, ts, []byte("tampered")); err == nil {
		t.Fatal("verify should fail when body changes")
	}
}

func TestVerifyRejectsReplay(t *testing.T) {
	t.Parallel()
	secret := "abc"
	body := []byte("payload")
	oldTs := time.Now().Add(-1 * time.Hour).UnixNano()
	sig := Sign(secret, oldTs, body)
	if err := Verify(secret, sig, oldTs, body); err == nil {
		t.Fatal("verify should reject stale timestamps")
	}
}

func TestVerifyRejectsEmptySecret(t *testing.T) {
	t.Parallel()
	if err := Verify("", "anysig", time.Now().UnixNano(), []byte("payload")); err == nil {
		t.Fatal("verify should refuse when secret is empty")
	}
}
