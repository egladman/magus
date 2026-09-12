package hint

import (
	"os"
	"strings"
	"sync"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The journal is append-only and bounded: the guard reads it for recency, so the file
// keeps the newest servedNextKept lines and drops the rest.
func TestServedNextJournalAppendsAndRotates(t *testing.T) {
	base := t.TempDir()
	one := []Next{breadcrumb("query-explain", Explain, "why", "spell:go")}

	for range servedNextKept + 5 {
		AppendServedNext(base, one)
	}

	raw, err := os.ReadFile(ServedNextPath(base))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Len(t, lines, servedNextKept)

	var got ServedNextEntry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &got))
	assert.Equal(t, ServedNextEntry{AtMs: got.AtMs, ID: "query-explain", Argv: []string{"magus", "explain", "spell:go"}}, got)
	assert.NotZero(t, got.AtMs)
}

// One journal per CHECKOUT: every door writes the file the guard reads, and nothing
// keys it by session.
func TestServedNextPathIsOnePerCheckout(t *testing.T) {
	base := t.TempDir()
	assert.Equal(t, ServedNextPath(base), ServedNextPath(base))
	assert.Contains(t, ServedNextPath(base), "served-next")
	assert.Empty(t, ServedNextPath(""))
}

// Nothing to write to, and nothing to write: both are silent no-ops, because a
// journal is not worth failing a query over.
func TestServedNextJournalIsBestEffort(t *testing.T) {
	AppendServedNext("", []Next{breadcrumb("query-explain", Explain, "why", "spell:go")})

	base := t.TempDir()
	AppendServedNext(base, nil)
	assert.Empty(t, ReadServedNext(base))
}

// A line nothing can be attributed to is a clearance nobody can audit, and a torn tail
// is ordinary on a file several processes append to.
func TestReadServedNextSkipsWhatItCannotAttribute(t *testing.T) {
	base := t.TempDir()
	AppendServedNext(base, []Next{breadcrumb("query-explain", Explain, "why", "spell:go")})

	f, err := os.OpenFile(ServedNextPath(base), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"ts":1,"id":"","argv":["magus","doctor"]}` + "\n" +
		`{"ts":2,"id":"no-argv","argv":[]}` + "\n" +
		`{"ts":3,"id":"tor`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got := ReadServedNext(base)
	require.Len(t, got, 1)
	assert.Equal(t, "query-explain", got[0].ID)
}

// Concurrent writers must not lose or tear a line. The guard clears a command on what
// it finds here, and `session hints` counts it, so a writer that drops entries under
// load makes both quietly wrong.
func TestServedNextJournalSurvivesConcurrentWriters(t *testing.T) {
	base := t.TempDir()
	const writers, each = 8, 10

	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				AppendServedNext(base, []Next{breadcrumb("query-explain", Explain, "why", "spell:go")})
			}
		}()
	}
	wg.Wait()

	raw, err := os.ReadFile(ServedNextPath(base))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Len(t, lines, writers*each, "every append has to survive; a lost line is a command magus served and then refuses")
	for _, line := range lines {
		var entry ServedNextEntry
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "torn line: %q", line)
		require.Equal(t, "query-explain", entry.ID)
	}
}

// The two sides agree on everything but the binary's spelling, which is the one part
// of a served line that changes nothing about what the command does.
func TestNormalizeServedArgvReducesTheBinary(t *testing.T) {
	want := []string{"magus", "explain", "spell:go"}
	for _, argv0 := range []string{"magus", "./magus", "/opt/bin/magus", `C:\tools\magus`} {
		assert.Equal(t, want, NormalizeServedArgv([]string{argv0, "explain", "spell:go"}), argv0)
	}
	assert.Nil(t, NormalizeServedArgv(nil))
}
