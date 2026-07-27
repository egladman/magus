package magus

import (
	"bytes"
	"testing"
	"time"

	json "github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
