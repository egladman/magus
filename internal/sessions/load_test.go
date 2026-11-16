package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstWordRejudge stands in for the guard's rules, which this package cannot import: it
// names the first word as the program and passes every command.
func firstWordRejudge(text string) (program, verdict, rule string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", session.VerdictPass, ""
	}
	return fields[0], session.VerdictPass, ""
}

// loadStream does what `magus session load --file` does: read the stream, then store
// what it held. The summary reports what the store did, as the command prints it.
func loadStream(t *testing.T, root string, lines ...string) (LoadSummary, error) {
	t.Helper()
	dir, err := Dir(root)
	require.NoError(t, err)

	summary, err := ReadLoadStream(strings.NewReader(strings.Join(lines, "\n")+"\n"), dir, firstWordRejudge)
	if err != nil {
		return summary, err
	}
	result, err := session.LoadEvents(t.Context(), dir, summary.Events, session.InvocationStart{Workspace: root, Command: "session load"})
	require.NoError(t, err)
	summary.Loaded, summary.Deduped, summary.Store = result.Loaded, result.Deduped, dir
	summary.ByKind = result.ByKind
	if len(summary.Rejects) > 0 {
		return summary, fmt.Errorf("%d line(s) rejected and not loaded: %s", len(summary.Rejects), strings.Join(summary.Rejects, ", "))
	}
	return summary, nil
}

// storeBytes is every byte the store holds, which is what the never-store-a-command
// rule has to hold over: a field that leaks the text leaks it into one of these files.
func storeBytes(t *testing.T, root string) string {
	t.Helper()
	dir, err := Dir(root)
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var b strings.Builder
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		b.Write(body)
	}
	return b.String()
}

func loadedEvent(t *testing.T, root, hostSession string, i int) session.AgentEvent {
	t.Helper()
	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(dir)
	require.NoError(t, err)
	events := session.AgentEvents(fold, hostSession)
	require.Greater(t, len(events), i)
	return events[i]
}

// TestLoadIsIdempotent is the property every adapter is built on: an adapter
// re-reads whole transcript files rather than tracking where it stopped, so the
// second pass over the same file has to be free.
func TestLoadIsIdempotent(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	lines := []string{
		`{"host":"h1","session":"s1","ts":1757430000000,"kind":"shell.command","ref":"r1","text":"ls -la"}`,
		`{"host":"h1","session":"s1","ts":1757430001000,"kind":"skill.load","ref":"r2","text":"magus-run"}`,
		`{"host":"h1","session":"s1","ts":1757430002000,"kind":"file.write","ref":"r3","text":"internal/foo.go"}`,
	}
	first, err := loadStream(t, root, lines...)
	require.NoError(t, err)
	assert.Equal(t, 3, first.Loaded)
	assert.Zero(t, first.Deduped)

	second, err := loadStream(t, root, lines...)
	require.NoError(t, err)
	assert.Zero(t, second.Loaded)
	assert.Equal(t, 3, second.Deduped)

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(dir)
	require.NoError(t, err)
	assert.Len(t, session.AgentEvents(fold, "s1"), 3, "a second load must not double the history")
}

func TestLoadDropsAnotherRepositorysEvents(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	elsewhere := t.TempDir()

	summary, err := loadStream(t, root,
		fmt.Sprintf("{\"host\":\"h1\",\"session\":\"s1\",\"ts\":1,\"cwd\":\"%s\",\"kind\":\"file.read\",\"ref\":\"r1\",\"text\":\"a.go\"}", root),
		fmt.Sprintf("{\"host\":\"h1\",\"session\":\"s1\",\"ts\":2,\"cwd\":\"%s\",\"kind\":\"file.read\",\"ref\":\"r2\",\"text\":\"b.go\"}", elsewhere))
	require.NoError(t, err)

	var out strings.Builder
	RenderLoadSummary(&out, summary)
	assert.Contains(t, out.String(), "loaded 1, deduped 0, dropped 1")
}

// TestLoadNeverStoresTheCommandText pins the rule the trail already settled:
// a command line is content, and content stays in the host's own transcript. The
// assertion is over the FILES rather than over the struct, because a field added
// later would satisfy a struct-shaped test and still write the line to disk.
func TestLoadNeverStoresTheCommandText(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	const secret = "curl -H 'Authorization: Bearer sk-not-a-real-token' https://example.com"
	_, err := loadStream(t, root,
		`{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"`+secret+`"}`)
	require.NoError(t, err)

	body := storeBytes(t, root)
	assert.NotContains(t, body, "Bearer", "a stored command line is a stored credential")
	assert.NotContains(t, body, "example.com")

	ev := loadedEvent(t, root, "s1", 0)
	assert.Equal(t, "curl", ev.Program)
	assert.Len(t, ev.Digest, 64, "the digest is what compares two commands nobody may read")
	assert.Empty(t, ev.Text)
}

// A rejected line names what is wrong with it and does not take the rest of the
// stream down: an adapter emitting one bad shape emits it for a whole transcript.
func TestLoadRejectsAnUnknownKind(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	summary, err := loadStream(t, root,
		`{"host":"h1","session":"s1","ts":1,"kind":"tool.use","ref":"r1","text":"x"}`,
		`{"host":"h1","session":"s1","ts":2,"kind":"file.read","ref":"r2","text":"a.go"}`,
		`{"host":"h1","ts":3,"kind":"file.read","ref":"r3","text":"b.go"}`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell.command", "the diagnostic names the set it accepts")
	assert.Contains(t, err.Error(), "no session")
	assert.Equal(t, 1, summary.Loaded, "the usable lines still load")
}

// TestReadLoadStreamSkipsAnOverlongLineInBoundedMemory is the bound: a line longer than
// any event is rejected by number and skipped, and the lines after it still load.
func TestReadLoadStreamSkipsAnOverlongLineInBoundedMemory(t *testing.T) {
	long := fmt.Sprintf("{\"event\":\"command\",\"host\":\"claude-code\",\"session\":\"s1\",\"cwd\":\"/nowhere\",\"command\":\"%s\"}", strings.Repeat("x", loadMaxLineBytes+1))

	summary, err := ReadLoadStream(strings.NewReader(fmt.Sprintf(`%s

%s
`, long, long)), t.TempDir(), firstWordRejudge)

	require.NoError(t, err)
	require.Len(t, summary.Rejects, 2)
	assert.Contains(t, summary.Rejects[0], "line 1: longer than any event")
	assert.Contains(t, summary.Rejects[1], "line 3: longer than any event")
}

// adapterCases holds one line per event kind, each the shape a shipped session-load
// adapter emitted against a synthetic host store. The fixture stays synthetic: a real
// transcript carries a person's own commands, paths and prompts, and a fixture is
// committed forever.
var adapterCases = map[string]string{
	session.EventSkillLoad:    `{"host":"claude-code","session":"s1","ts":1788955200100,"kind":"skill.load","ref":"t1","text":"magus-run","outcome":{"exit":null,"denied":false,"interrupted":false}}`,
	session.EventShellCommand: `{"host":"claude-code","session":"s1","ts":1788955202000,"kind":"shell.command","ref":"t2","text":"blocked-command","outcome":{"exit":null,"denied":true,"interrupted":false}}`,
	session.EventHookOutput:   `{"host":"claude-code","session":"s1","ts":1788955202500,"kind":"hook.output","ref":"t2","text":"the guard said so"}`,
	session.EventSpawn:        `{"host":"claude-code","session":"s1","ts":1788955204000,"kind":"spawn","ref":"t3","text":"Explore"}`,
	session.EventFileRead:     `{"host":"claude-code","session":"s1","ts":1788955206000,"kind":"file.read","ref":"t5","text":"README.md"}`,
	session.EventFileWrite:    `{"host":"claude-code","session":"s1","ts":1788955207000,"kind":"file.write","ref":"t6","text":"AGENTS.md"}`,
	session.EventMagusCall:    `{"host":"claude-code","session":"s1","ts":1788955208000,"kind":"magus.call","ref":"t7","text":"mcp__magus__magus_query"}`,
}

// A kind the session contract gains with no adapter case is a kind no load has
// ever been shown to store.
func TestSessionAdapterCasesCoverEveryKind(t *testing.T) {
	for _, kind := range session.EventKinds() {
		assert.Contains(t, adapterCases, kind,
			"adapterCases has no line for %q: every kind in the session contract needs one", kind)
	}
}

func TestLoadStoresEveryAdapterCase(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	lines := make([]string, 0, len(adapterCases))
	for _, kind := range session.EventKinds() {
		lines = append(lines, adapterCases[kind])
	}
	summary, err := loadStream(t, root, lines...)
	require.NoError(t, err)

	want := map[string]int{}
	for _, kind := range session.EventKinds() {
		want[kind] = 1
	}
	assert.Equal(t, want, summary.ByKind, "each adapter line loads as the kind it names")
	command := loadedEvent(t, root, "s1", 0)
	require.Equal(t, session.EventShellCommand, command.Kind)
	assert.True(t, command.Denied, "the denial rides on the command it belongs to")
}
