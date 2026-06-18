package report

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWriter_NonNil(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	require.NotNil(t, w, "NewWriter returned nil")
	w.Close()
}

func TestWriter_Stats_InitialZero(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	defer w.Close()

	s := w.Stats()
	assert.Zero(t, s.Recorded, "Stats.Recorded should be 0 before any writes")
	assert.NoError(t, s.LastErr, "Stats.LastErr should be nil before any writes")
}

func TestWriter_Close_NoError(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	assert.NoError(t, w.Close())
}

func TestWriter_RecordAndClose(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull())
	require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "build"}))
	require.NoError(t, w.Close())
	out := buf.String()
	assert.Contains(t, out, `"schema"`, "output missing schema field")
	assert.Contains(t, out, "target.result", "output missing event type")
}

func TestWriter_WithQueueSize(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, WithQueueSize(4))
	require.NotNil(t, w, "NewWriter with custom queue returned nil")
	w.Close()
}
