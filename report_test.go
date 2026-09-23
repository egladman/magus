package magus

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedWriter blocks every Write until open is closed, standing in for a reader that
// falls behind.
type gatedWriter struct {
	open chan struct{}
	buf  bytes.Buffer
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	<-g.open
	return g.buf.Write(p)
}

// A reader that falls behind slows the run down; it never loses a record. Past the
// queue's 4096 slots the old writer dropped events, and a failed target's only
// run.target.result could be one of them.
func TestReportWriterNeverDropsARecord(t *testing.T) {
	dst := &gatedWriter{open: make(chan struct{})}
	rw, err := NewReportWriter(dst, nil)
	require.NoError(t, err)
	const n = 10_000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range n {
			_ = rw.RecordNotice(slog.LevelInfo, "", "x")
		}
	}()
	time.Sleep(50 * time.Millisecond)
	close(dst.open)
	<-done
	require.NoError(t, rw.Close())
	assert.Equal(t, n, strings.Count(dst.buf.String(), "\n"))
	assert.NotContains(t, dst.buf.String(), "dropped")
}

func TestReportWriterRecordsShardTotal(t *testing.T) {
	var dst bytes.Buffer
	writer, err := NewReportWriter(&dst, []string{report.TypeShardTotal})
	require.NoError(t, err)
	require.NoError(t, writer.RecordShardTotal("2", 4, 1500*time.Millisecond))
	require.NoError(t, writer.Close())

	var got struct {
		Schema int    `json:"schema"`
		Type   string `json:"type"`
		report.ShardTotal
	}
	require.NoError(t, json.Unmarshal(dst.Bytes(), &got))
	assert.Equal(t, struct {
		Schema int    `json:"schema"`
		Type   string `json:"type"`
		report.ShardTotal
	}{
		Schema:     report.Schema,
		Type:       report.TypeShardTotal,
		ShardTotal: report.ShardTotal{Shard: "2", NShards: 4, DurationMs: 1500},
	}, got)
}
