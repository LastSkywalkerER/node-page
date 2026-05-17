package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DefaultSubmitTimeout is used by SubmitTyped when the caller passes 0.
const DefaultSubmitTimeout = 5 * time.Second

// SubmitTyped is the canonical helper used by services to translate a
// repository write into a Raft command. It JSON-encodes payload, stamps
// Timestamp from svc.Clock() if zero and submits as the given command type.
//
// When svc is a DisabledService it returns (zero, ErrDisabled) — callers
// should fall back to the legacy direct-write path.
//
// The returned SubmitResult.Err carries deterministic FSM errors (e.g.
// "user already exists"); callers should map those to their own business
// errors via errors.Is.
func SubmitTyped(ctx context.Context, svc Service, t CommandType, payload any, timeout time.Duration) (SubmitResult, error) {
	if svc == nil || !svc.Enabled() {
		return SubmitResult{}, ErrDisabled
	}
	if timeout <= 0 {
		timeout = DefaultSubmitTimeout
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("raft: encode payload: %w", err)
	}
	cmd := Command{Type: t, Timestamp: time.Now().UTC(), Payload: data}
	return svc.SubmitCommand(ctx, cmd, timeout)
}

// DecodeTyped is the applier-side counterpart of SubmitTyped: it unmarshals
// the JSON payload into the destination struct.
func DecodeTyped(cmd Command, out any) error {
	if len(cmd.Payload) == 0 {
		return errors.New("raft: empty payload")
	}
	return json.Unmarshal(cmd.Payload, out)
}
