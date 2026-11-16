package sessions

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shownSession loads lines into a fresh store and renders what `session show` prints for s1.
func shownSession(t *testing.T, lines ...string) string {
	t.Helper()
	testkit.Isolate(t)
	root := t.TempDir()
	_, err := loadStream(t, root, lines...)
	require.NoError(t, err)

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(dir)
	require.NoError(t, err)

	var out strings.Builder
	RenderShow(&out, SummarizeSession("s1", session.AgentEvents(fold, "s1")))
	return out.String()
}

func TestShowGroupsCommandsByProgram(t *testing.T) {
	out := shownSession(t,
		`{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"go build ./...","outcome":{"denied":true}}`,
		`{"host":"h1","session":"s1","ts":2,"kind":"shell.command","ref":"r2","text":"go test ./..."}`,
		`{"host":"h1","session":"s1","ts":3,"kind":"skill.load","ref":"r3","text":"magus-run"}`,
		`{"host":"h1","session":"s1","ts":4,"kind":"file.write","ref":"r4","text":"internal/foo.go"}`,
		`{"host":"h1","session":"s1","ts":5,"kind":"hook.output","ref":"r5","text":"deny: use magus"}`)

	assert.Contains(t, out, "host h1")
	assert.Contains(t, out, "go ")
	assert.Contains(t, out, "pass 2", "the stand-in rules pass both commands")
	assert.Contains(t, out, "host recorded 1 denied")
	assert.Contains(t, out, "magus-run")
	assert.Contains(t, out, "internal/foo.go")
	assert.Contains(t, out, "hook outputs: 1")
}

// The pair `show` prints is the NEWEST one a loaded event named, not the first: a
// session that ran under one model and switched mid-session is checked against
// what it ended on.
func TestShowPrintsTheModelAndHostVersionPair(t *testing.T) {
	out := shownSession(t,
		`{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"ls","agent":{"model":"claude-opus-5","host_version":"2.1.260"}}`,
		`{"host":"h1","session":"s1","ts":2,"kind":"shell.command","ref":"r2","text":"pwd","agent":{"model":"claude-opus-5-5","host_version":"2.1.280"}}`)

	assert.Contains(t, out, "model claude-opus-5-5  host version 2.1.280")
}

// An adapter that cannot know the model or the host version omits the field
// (or sends it null); `show` reports the pair as absent rather than printing an
// empty line nobody asked for.
func TestShowOmitsThePairLineWhenNoEventNamesOne(t *testing.T) {
	out := shownSession(t,
		`{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"ls"}`)

	assert.NotContains(t, out, "host version")
}
