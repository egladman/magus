package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveNext writes one journal entry in the shape the serving side appends, so the two
// sides of the contract are pinned by the same literal a reader can compare to the docs.
func serveNext(t *testing.T, gate advisoryGate, id string, argv ...string) {
	t.Helper()
	path := gate.servedNextPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	line := fmt.Sprintf(`{"ts":%d,"id":%q,"argv":[%s]}`+"\n",
		time.Now().UnixMilli(), id, strings.Join(quoted, ","))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(line)
	require.NoError(t, err)
}

// TestServedNextPreauthorizesTheCommandItServed pins the match, the binary spellings it
// has to see through, and the id it hands back for the metric.
func TestServedNextPreauthorizesTheCommandItServed(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	serveNext(t, gate, "query-explain", "magus", "explain", "target:.:ci")

	for _, command := range []string{
		"magus explain target:.:ci",
		"./magus explain target:.:ci",
		"/opt/bin/magus explain target:.:ci",
	} {
		assert.Equal(t, "query-explain", servedNextPreauthorizes(gate, command), "%q", command)
	}
}

// TestServedNextPreauthorizesNothingElse is the whole safety argument for the rule: the
// clearance covers the command that was served and not one token more.
func TestServedNextPreauthorizesNothingElse(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	serveNext(t, gate, "query-explain", "magus", "explain", "target:.:ci")

	for name, command := range map[string]string{
		"a different argument":     "magus explain target:.:lint",
		"an extra flag":            "magus explain target:.:ci -o json",
		"a missing argument":       "magus explain",
		"another command chained":  "magus explain target:.:ci && git stash",
		"a pipe":                   "magus explain target:.:ci | head",
		"a different program":      "notmagus explain target:.:ci",
		"a line that does not run": "",
	} {
		assert.Empty(t, servedNextPreauthorizes(gate, command), name)
	}

	assert.Empty(t, servedNextPreauthorizes(advisoryGate{}, "magus explain target:.:ci"),
		"no workspace, no journal, nothing pre-authorized")
	assert.Empty(t, servedNextPreauthorizes(newAdvisoryGate(t.TempDir(), "other"), "magus explain target:.:ci"),
		"the journal is per session: another session's servings clear nothing here")
}

// TestReadServedNextIsRobustAndBounded covers the three things a journal appended to by a
// concurrent process does: it grows past the window, it carries a torn line, and it
// carries an entry naming no template.
func TestReadServedNextIsRobustAndBounded(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	for i := range servedNextWindow + 5 {
		serveNext(t, gate, "run-output", "magus", "query", "output", fmt.Sprintf("ref-%d", i))
	}
	assert.Len(t, readServedNext(gate), servedNextWindow,
		"only the recent window stays cleared")
	assert.Empty(t, servedNextPreauthorizes(gate, "magus query output ref-0"),
		"an entry that has aged out of the window is no longer magus's current suggestion")
	assert.Equal(t, "run-output", servedNextPreauthorizes(gate, "magus query output ref-24"))

	f, err := os.OpenFile(gate.servedNextPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"ts":1,"id":"","argv":["magus","doctor"]}` + "\n" + `{"ts":2,"id":"tor`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.Empty(t, servedNextPreauthorizes(gate, "magus doctor"),
		"an entry naming no template is a clearance nobody can count")
	assert.Equal(t, "run-output", servedNextPreauthorizes(gate, "magus query output ref-24"),
		"a torn final line must not stop the entries behind it from clearing")
	assert.Empty(t, readServedNext(newAdvisoryGate(filepath.Join(t.TempDir(), "absent"), "s")),
		"a missing journal pre-authorizes nothing")
}

// TestServedNextReadsTheAnonymousJournal covers the producer that has no session id at
// all: `next` printed by a CLI run lands in the anonymous bucket, and reading only the
// session-keyed file would pre-authorize nothing an agent actually saw.
func TestServedNextReadsTheAnonymousJournal(t *testing.T) {
	base := t.TempDir()
	cli := newAdvisoryGate(base, "")
	serveNext(t, cli, "query-explain", "magus", "explain", "target:.:ci")

	hook := newAdvisoryGate(base, "session-1")
	assert.Equal(t, "query-explain", servedNextPreauthorizes(hook, "./magus explain target:.:ci"),
		"the hook reports a session id; the CLI run that printed the breadcrumb did not")

	serveNext(t, hook, "query-refs", "magus", "refs", "hookCmd")
	assert.Equal(t, "query-refs", servedNextPreauthorizes(hook, "magus refs hookCmd"),
		"the session's own journal still clears")
	assert.Equal(t, "query-explain", servedNextPreauthorizes(hook, "magus explain target:.:ci"),
		"and reading one does not shadow the other")
}

// TestHookCmdStandsDownOnAServedNext is the rule in place: the same command is denied by a
// role-scoped rule and cleared once magus is the one that suggested it.
func TestHookCmdStandsDownOnAServedNext(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t, narrowLease())
	gate := newAdvisoryGate(hookActivityTrail(ctx).base, "session-preauth")
	command := "./magus affected ci --no-default-charms"

	var denied bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(command), &denied,
		[]string{"--lease", narrowLease().ID, "--session", "session-preauth", "-o", "name"})
	require.Error(t, err, "the gate rule refuses a lease that was handed a narrower check")
	assert.Equal(t, "deny\n", denied.String())

	serveNext(t, gate, "gate-run", "magus", "affected", "ci", "--no-default-charms")

	var cleared bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(command), &cleared,
		[]string{"--lease", narrowLease().ID, "--session", "session-preauth", "-o", "name"}))
	assert.Equal(t, "pass\n", cleared.String(),
		"magus refusing the command magus served is the tool disagreeing with itself")
}

// TestHookCmdNeverPreauthorizesAWorkspaceWideDeny is the carve-out. These refuse work that
// cannot be undone or that silently discards an exit status, and they protect everyone, so
// a journal entry naming one buys nothing.
func TestHookCmdNeverPreauthorizesAWorkspaceWideDeny(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t)
	gate := newAdvisoryGate(hookActivityTrail(ctx).base, "session-carveout")

	for _, command := range []string{"git stash", "magus affected ci | tail -5", "go test ./..."} {
		serveNext(t, gate, "fabricated", strings.Fields(command)...)
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(command), &out,
			[]string{"--session", "session-carveout", "-o", "name"})
		require.Error(t, err, "%q", command)
		assert.Equal(t, "deny\n", out.String(), "%q", command)
	}
}
