// Package logbuffer keeps a bounded, in-memory ring of the most recent process
// log lines so the app can surface its own logs in the UI. This is especially
// handy for native (non-Docker) installs where `docker logs` isn't available
// and operators would otherwise need shell access to read journald/stderr.
//
// The ring is fixed-size (memory-stable) in line with the project's
// keep-resident-memory-low convention: it never grows past its line cap and is
// never persisted to the database or Raft snapshots.
package logbuffer

import (
	"strings"
	"sync"
)

// DefaultMaxLines is the ring capacity used by the package-level Default buffer.
const DefaultMaxLines = 2000

// Default is the process-wide buffer. server.go attaches it to the logger via
// io.MultiWriter and the logs endpoint reads from it.
var Default = New(DefaultMaxLines)

// Buffer is a thread-safe ring of recent log lines. It implements io.Writer so
// it can be attached to a charmbracelet/log logger through io.MultiWriter.
type Buffer struct {
	mu   sync.Mutex
	buf  []string
	max  int
	head int // index of the oldest line
	size int // number of valid lines currently stored
	// carry holds the tail of a Write that didn't end in '\n', so a log entry
	// split across two Write calls is still stored as a single line.
	carry string
}

// New returns a ring buffer holding at most max lines (falls back to
// DefaultMaxLines when max <= 0).
func New(max int) *Buffer {
	if max <= 0 {
		max = DefaultMaxLines
	}
	return &Buffer{buf: make([]string, max), max: max}
}

// Write implements io.Writer. It splits the incoming bytes on newlines and
// stores each completed line, carrying any trailing partial line forward.
// Always reports the full length written so it never short-circuits a
// MultiWriter.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.carry + string(p)
	b.carry = ""
	parts := strings.Split(s, "\n")
	// The final element is the remainder after the last '\n': "" when the input
	// ended cleanly, otherwise a partial line to carry into the next Write.
	b.carry = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		b.append(line)
	}
	return len(p), nil
}

// append stores one line, overwriting the oldest when at capacity.
func (b *Buffer) append(line string) {
	if b.size < b.max {
		b.buf[(b.head+b.size)%b.max] = line
		b.size++
		return
	}
	b.buf[b.head] = line
	b.head = (b.head + 1) % b.max
}

// String returns all buffered lines, oldest first, joined by '\n'.
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == 0 {
		return ""
	}
	out := make([]string, b.size)
	for i := 0; i < b.size; i++ {
		out[i] = b.buf[(b.head+i)%b.max]
	}
	return strings.Join(out, "\n")
}

// Len returns the number of lines currently buffered.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}
