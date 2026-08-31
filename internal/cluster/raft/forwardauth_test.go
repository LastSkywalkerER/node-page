package raft

import "testing"

func TestSignVerifyForwardRoundTrip(t *testing.T) {
	body := []byte(`{"type":40,"payload":"x"}`)
	var ts int64 = 1700000000000000000
	sig := signForward("secret", ts, body)
	if err := verifyForward("secret", sig, ts, body); err != nil {
		t.Fatalf("valid signature must verify: %v", err)
	}
}

func TestVerifyForwardRejectsTamperedBody(t *testing.T) {
	var ts int64 = 42
	sig := signForward("secret", ts, []byte("original"))
	if err := verifyForward("secret", sig, ts, []byte("tampered")); err == nil {
		t.Fatal("a signature must not verify against a different body")
	}
}

func TestVerifyForwardRejectsWrongSecret(t *testing.T) {
	body := []byte("b")
	var ts int64 = 42
	sig := signForward("secretA", ts, body)
	if err := verifyForward("secretB", sig, ts, body); err == nil {
		t.Fatal("a signature must not verify under a different secret")
	}
}

func TestVerifyForwardRejectsTimestampReuseOnDifferentBody(t *testing.T) {
	// The timestamp is folded into the MAC, so a captured signature can't be
	// replayed onto another body even with the same ts.
	var ts int64 = 99
	sig := signForward("s", ts, []byte("cmd-A"))
	if err := verifyForward("s", sig, ts, []byte("cmd-B")); err == nil {
		t.Fatal("signature bound to body A must not verify body B")
	}
}

func TestVerifyForwardEmptyInputs(t *testing.T) {
	if err := verifyForward("", "sig", 1, []byte("b")); err == nil {
		t.Fatal("empty secret must fail")
	}
	if err := verifyForward("s", "", 1, []byte("b")); err == nil {
		t.Fatal("empty signature must fail")
	}
}
