package setup

import "testing"

func TestParsePeerBridge(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantNil  bool
		secret   string
		seedsLen int
	}{
		{
			name:     "full uplink",
			body:     `{"cluster_id":"sky-home","bridge":{"enabled":true,"shared_secret":"s3cr3t","remote_seeds":["https://hub.example"]}}`,
			secret:   "s3cr3t",
			seedsLen: 1,
		},
		{name: "no bridge field", body: `{"cluster_id":"sky-home"}`, wantNil: true},
		{name: "disabled", body: `{"bridge":{"enabled":false,"shared_secret":"s","remote_seeds":["x"]}}`, wantNil: true},
		{name: "empty secret", body: `{"bridge":{"enabled":true,"shared_secret":"","remote_seeds":["x"]}}`, wantNil: true},
		{name: "no seeds", body: `{"bridge":{"enabled":true,"shared_secret":"s","remote_seeds":[]}}`, wantNil: true},
		{name: "garbage", body: `not json`, wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePeerBridge([]byte(tc.body))
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected a bridge, got nil")
			}
			if got.SharedSecret != tc.secret {
				t.Fatalf("secret = %q, want %q", got.SharedSecret, tc.secret)
			}
			if len(got.RemoteSeeds) != tc.seedsLen {
				t.Fatalf("seeds len = %d, want %d", len(got.RemoteSeeds), tc.seedsLen)
			}
		})
	}
}
