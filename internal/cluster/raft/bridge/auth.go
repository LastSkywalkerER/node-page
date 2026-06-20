package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// HMACHeader is the request header carrying the signature.
const HMACHeader = "X-Bridge-Signature"

// TimestampHeader carries the unix nanosecond timestamp the sender stamped
// on the request. Replays older than 5 minutes are rejected.
const TimestampHeader = "X-Bridge-Timestamp"

// SenderClusterHeader carries the sender's cluster id so the receiver can
// dedupe per origin.
const SenderClusterHeader = "X-Bridge-Cluster-ID"

// SenderNodeHeader carries the sender's node id (informational; logs).
const SenderNodeHeader = "X-Bridge-Node-ID"

// MaxClockSkew is the largest tolerated drift between sender and receiver
// clocks. Replays older than this fail HMAC verification regardless of
// signature correctness.
const MaxClockSkew = 5 * time.Minute

// Sign produces hex(HMAC-SHA256(secret, tsNanos || body)) — the receiver
// recomputes the same thing and uses hmac.Equal to compare in constant time.
func Sign(secret string, tsNanos int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(tsNanos, 10)))
	_, _ = mac.Write([]byte{':'})
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks the signature on a received bridge request. Returns nil
// when both the timestamp is fresh (within MaxClockSkew) AND the HMAC matches.
// Use this for the cross-cluster bridge (untrusted internet path).
func Verify(secret, header string, tsNanos int64, body []byte) error {
	return VerifyWithSkew(secret, header, tsNanos, body, MaxClockSkew)
}

// VerifyWithSkew is Verify with an explicit freshness window. A maxSkew <= 0
// SKIPS the clock-skew check entirely and only verifies the HMAC — used for the
// intra-cluster metric stream, a trusted authenticated LAN channel where a
// replay just re-ingests the node's current metrics (harmless, overwritten next
// tick). This keeps cross-node metrics flowing even when peers' clocks drift
// (common on SBCs / containers without reliable NTP), which would otherwise
// reject every batch with "timestamp out of allowed clock skew".
func VerifyWithSkew(secret, header string, tsNanos int64, body []byte, maxSkew time.Duration) error {
	if secret == "" {
		return errors.New("bridge: shared secret not configured")
	}
	if header == "" {
		return errors.New("bridge: missing signature header")
	}
	if maxSkew > 0 {
		now := time.Now().UnixNano()
		if abs(now-tsNanos) > int64(maxSkew) {
			return errors.New("bridge: timestamp out of allowed clock skew")
		}
	}
	expected := Sign(secret, tsNanos, body)
	if !hmac.Equal([]byte(expected), []byte(header)) {
		return errors.New("bridge: signature mismatch")
	}
	return nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
