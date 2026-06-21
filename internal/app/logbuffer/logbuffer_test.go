package logbuffer

import "testing"

func TestBufferSplitsAndCarries(t *testing.T) {
	b := New(10)
	// A single Write may contain several lines plus a trailing partial.
	_, _ = b.Write([]byte("line1\nline2\npart"))
	if got := b.String(); got != "line1\nline2" {
		t.Fatalf("after partial write got %q, want %q", got, "line1\nline2")
	}
	// The carried partial completes on the next Write.
	_, _ = b.Write([]byte("ial3\nline4\n"))
	if got := b.String(); got != "line1\nline2\npartial3\nline4" {
		t.Fatalf("after completing carry got %q", got)
	}
	if b.Len() != 4 {
		t.Fatalf("Len = %d, want 4", b.Len())
	}
}

func TestBufferRingEvictsOldest(t *testing.T) {
	b := New(3)
	for _, l := range []string{"a\n", "b\n", "c\n", "d\n", "e\n"} {
		_, _ = b.Write([]byte(l))
	}
	if got, want := b.String(), "c\nd\ne"; got != want {
		t.Fatalf("ring eviction got %q, want %q", got, want)
	}
	if b.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (capped)", b.Len())
	}
}

func TestBufferEmpty(t *testing.T) {
	b := New(5)
	if got := b.String(); got != "" {
		t.Fatalf("empty buffer String = %q, want empty", got)
	}
}
