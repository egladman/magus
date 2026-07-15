package console

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogViewerURL builds the log-viewer deep link: the ref identity AND the
// gzip+base64url Journal both ride the URL fragment (after #), which the browser never
// transmits - so nothing about the run, not even its ref, leaves the machine.
func TestLogViewerURL(t *testing.T) {
	const base = "https://eli.gladman.cc/magus/logs/"
	events := []journal.Event{
		{Ts: 1, Kind: journal.KindOutput, Stream: journal.StreamStdout, Text: "build failed: boom"},
		{Ts: 2, Kind: journal.KindResult, Status: journal.StatusFail, Ref: "outdeadbeef"},
	}
	url, err := LogViewerURL(base, "outdeadbeef", events,
		journal.Invocation{ID: "inv1", Command: journal.Command{Arguments: []string{"run", "build"}}})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(url, base+"#ref=outdeadbeef&data="),
		"url should carry the ref identity then the fragment data, got %q", url)
	// Everything rides after #, which browsers never send to the server: no query string.
	before, after, found := strings.Cut(url, "#")
	require.True(t, found, "url must have a fragment")
	assert.NotContains(t, before, "?", "nothing must ride the query string")
	assert.Contains(t, after, "ref=outdeadbeef", "ref must live in the fragment")
	assert.Contains(t, after, "data=", "data must live in the fragment")
}
