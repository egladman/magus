package interp

import (
	"path/filepath"
	"testing"
)

func TestAppendAndLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")

	h, err := Open(path, 5)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	h.Append("one")
	h.Append("two")
	h.Append("two") // duplicate of previous → skipped
	h.Append("three")

	lines := h.Lines()
	if got, want := len(lines), 3; got != want {
		t.Fatalf("len(lines)=%d, want %d (%v)", got, want, lines)
	}
	if lines[0] != "one" || lines[1] != "two" || lines[2] != "three" {
		t.Errorf("lines = %v", lines)
	}
}

func TestRecall(t *testing.T) {
	dir := t.TempDir()
	h, err := Open(filepath.Join(dir, "hist"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.Append("first")
	h.Append("second")
	h.Append("third")

	if got := h.Recall(1); got != "third" {
		t.Errorf("Recall(1)=%q, want %q", got, "third")
	}
	if got := h.Recall(3); got != "first" {
		t.Errorf("Recall(3)=%q, want %q", got, "first")
	}
	if got := h.Recall(99); got != "" {
		t.Errorf("Recall(99)=%q, want empty", got)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")

	h, _ := Open(path, 0)
	h.Append("alpha")
	h.Append("beta")

	// New History opened against the same file should pick the lines back up.
	h2, err := Open(path, 0)
	if err != nil {
		t.Fatalf("Open second time: %v", err)
	}
	if got := h2.Lines(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("reopened lines = %v", got)
	}
}

func TestCapOverflowTrims(t *testing.T) {
	dir := t.TempDir()
	h, _ := Open(filepath.Join(dir, "hist"), 3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		h.Append(s)
	}
	got := h.Lines()
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (%v)", len(got), got)
	}
	if got[0] != "c" || got[1] != "d" || got[2] != "e" {
		t.Errorf("lines = %v, want [c d e]", got)
	}
}
