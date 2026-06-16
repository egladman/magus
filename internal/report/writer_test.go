package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewWriter_NonNil(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if w == nil {
		t.Fatal("NewWriter returned nil")
	}
	w.Close()
}

func TestWriter_Stats_InitialZero(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	defer w.Close()

	s := w.Stats()
	if s.Recorded != 0 {
		t.Errorf("Stats.Recorded = %d, want 0 before any writes", s.Recorded)
	}
	if s.LastErr != nil {
		t.Errorf("Stats.LastErr = %v, want nil before any writes", s.LastErr)
	}
}

func TestWriter_Close_NoError(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWriter_RecordAndClose(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull())
	if err := Record(w, CacheHit{Project: "p", Target: "build"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"schema"`) {
		t.Errorf("output %q missing schema field", out)
	}
	if !strings.Contains(out, "cache.hit") {
		t.Errorf("output %q missing event type", out)
	}
}

func TestWriter_WithQueueSize(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, WithQueueSize(4))
	if w == nil {
		t.Fatal("NewWriter with custom queue returned nil")
	}
	w.Close()
}
