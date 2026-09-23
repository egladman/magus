package report

import (
	"bytes"
	"fmt"
	"strings"
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
	assert.Contains(t, out, TypeTargetResult, "output missing event type")
}

// gatedWriter holds every Write until open is closed, then keeps what it is given.
type gatedWriter struct {
	open chan struct{}
	buf  bytes.Buffer
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	<-g.open
	return g.buf.Write(p)
}

// A writer that did drop events says so, as the stream's last record, so a reader learns
// the stream is incomplete from the stream itself.
func TestWriter_CloseRecordsTheDropCount(t *testing.T) {
	t.Parallel()
	g := &gatedWriter{open: make(chan struct{})}
	w := NewWriter(g, func(c *writerCfg) { c.queueSize = 4 })
	for range 200 {
		_ = Record(w, TargetResult{Status: "ok", Project: "p", Target: "t"})
	}
	close(g.open)
	require.NoError(t, w.Close())
	dropped := w.Stats().Dropped
	require.NotZero(t, dropped)
	lines := strings.Split(strings.TrimSpace(g.buf.String()), "\n")
	assert.Equal(t, fmt.Sprintf(`{"schema":5,"type":"run.notice","level":"error","msg":"report: %d events dropped"}`, dropped), lines[len(lines)-1])
}
