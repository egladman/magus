package report

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reader hands back each record once and in order, keeps what it cannot parse for a
// person instead of failing on it, and reports the end of the stream.
func TestReaderReadsRecordsInOrder(t *testing.T) {
	src := `{"schema":5,"type":"run.scope","label":"api","projects":["api"]}
not a record
{"schema":5,"type":"run.target.result","project":"api","target":"build","status":"ok","cache_hit":false}
`
	var stray bytes.Buffer
	r := NewReader(strings.NewReader(src), &stray)

	first, ok, err := r.Next(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, Line{Schema: 5, Type: TypeRunScope, Raw: []byte(`{"schema":5,"type":"run.scope","label":"api","projects":["api"]}`)}, first)
	var scope RunScope
	require.NoError(t, first.Decode(&scope))
	assert.Equal(t, RunScope{Label: "api", Projects: []string{"api"}}, scope)

	rest, err := r.Rest(t.Context())
	require.NoError(t, err)
	require.Len(t, rest, 1)
	assert.Equal(t, TypeTargetResult, rest[0].Type)

	_, ok, err = r.Next(t.Context())
	require.NoError(t, err)
	assert.False(t, ok, "the stream ended")

	all, err := r.All(t.Context())
	require.NoError(t, err)
	assert.Len(t, all, 2, "All includes records already returned")
	assert.Equal(t, "not a record\n", stray.String())
}

// Next waits for a record the writer has yet to write, and gives up with its context.
func TestReaderNextBlocksUntilARecordOrCancel(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr, nil)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = pw.Write([]byte(`{"schema":5,"type":"run.scope","label":"x"}` + "\n"))
	}()
	line, ok, err := r.Next(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, TypeRunScope, line.Type)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, _, err = r.Next(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_ = pw.Close()
}

// A line passed through a stage reaches the next one byte for byte.
func TestEncodeLinePassesARecordThrough(t *testing.T) {
	raw := `{"schema":5,"type":"run.notice","level":"info","msg":"x"}`
	line, err := ParseLine([]byte(raw + "\n"))
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, NewLineEncoder(&out).EncodeLine(line))
	assert.Equal(t, raw+"\n", out.String())

	_, err = ParseLine([]byte(`{"schema":5}`))
	assert.ErrorContains(t, err, "no type")
	_, err = ParseLine([]byte(`[1]`))
	assert.Error(t, err)
}
