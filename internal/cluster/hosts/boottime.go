package hosts

// BootTimeJitterSeconds bounds the truncation noise of a boot time derived as
// "now − uptime" from two whole-second clocks sampled a request apart (the
// Proxmox/PBS status calls, gopsutil's /proc/uptime path inside a container).
// Consecutive polls of the same machine legitimately disagree by ±1 s.
const BootTimeJitterSeconds = 2

// StableBootTime decides which boot time a host row keeps when a new value
// arrives. 0 means "unknown" (a writer that never had it — a backfill from a
// table row, an offline hypervisor node, a failed status call) and must never
// erase a known value: the replicated record would then fall back to nothing
// and the card would show no uptime (or, historically, the SERVER's process
// uptime — the same number on every remote machine). A value that differs from
// the stored one only by truncation noise is also kept as stored, so the
// record fingerprint (and with it a Raft consensus round) doesn't flip on
// every poll for a machine whose boot time hasn't changed.
func StableBootTime(existing, incoming int64) int64 {
	if incoming <= 0 {
		return existing
	}
	if existing > 0 {
		d := incoming - existing
		if d < 0 {
			d = -d
		}
		if d <= BootTimeJitterSeconds {
			return existing
		}
	}
	return incoming
}
