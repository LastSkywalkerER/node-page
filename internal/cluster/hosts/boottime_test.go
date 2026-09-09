package hosts

import "testing"

func TestStableBootTime(t *testing.T) {
	const stored = int64(1_700_000_000)
	cases := []struct {
		name     string
		existing int64
		incoming int64
		want     int64
	}{
		{"unknown incoming keeps stored", stored, 0, stored},
		{"negative incoming keeps stored", stored, -5, stored},
		{"nothing stored adopts incoming", 0, stored, stored},
		{"nothing anywhere stays unknown", 0, 0, 0},
		{"same value", stored, stored, stored},
		{"+1s truncation noise keeps stored", stored, stored + 1, stored},
		{"-1s truncation noise keeps stored", stored, stored - 1, stored},
		{"at the jitter bound keeps stored", stored, stored + BootTimeJitterSeconds, stored},
		{"beyond the bound is a real reboot", stored, stored + BootTimeJitterSeconds + 1, stored + BootTimeJitterSeconds + 1},
		{"a much later boot wins", stored, stored + 86_400, stored + 86_400},
		{"an earlier boot (clock fixed) wins", stored, stored - 3_600, stored - 3_600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StableBootTime(tc.existing, tc.incoming); got != tc.want {
				t.Fatalf("StableBootTime(%d, %d) = %d, want %d", tc.existing, tc.incoming, got, tc.want)
			}
		})
	}
}
