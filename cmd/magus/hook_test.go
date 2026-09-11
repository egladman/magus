package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// verdictDecisionRe finds every decision literal the guard assigns, in either
// spelling the code uses: the struct-literal `Decision: "pass"` and the
// assignment `verdict.Decision = "deny"`.
var verdictDecisionRe = regexp.MustCompile(`Decision(?::|\s*=)\s*"(\w+)"`)

// fleetFixture stands up a workspace root and a lease ledger holding leases, and
// returns the context pinning both, the workspace root, and the resolved cache dir.
// Mirrors internal/guard's own fleetFixture through the exported guard.WithLocation
// seam, since this package cannot reach the unexported hookActivityLocationKey it
// uses directly.
func fleetFixture(t *testing.T, leases ...types.Lease) (ctx context.Context, root, cacheDir string) {
	t.Helper()
	// The ledger now lives in the per-repository state directory, and the guard resolves
	// it with no seam a test can reach, so the environment is what keeps this off the
	// developer's own ledger.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, cacheDir = t.TempDir(), t.TempDir()
	store := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root})
	for _, u := range leases {
		_, err := store.Update(t.Context(), u.ID, func(cur *types.Lease) { *cur = u })
		require.NoError(t, err)
	}
	return guard.WithLocation(t.Context(), cacheDir, root, ""), root, cacheDir
}

// fleetLeases mirrors internal/guard's own fleetLeases, unexported there: two live
// workers with disjoint write paths, one of them declaring a denied subtree inside its
// own.
func fleetLeases() []types.Lease {
	return []types.Lease{
		{
			ID:           "lease-a",
			Goal:         "own the ledger store\nacceptance: List stays cheap",
			WritePaths:   []string{"internal/ledger/**"},
			State:        types.StateRunning,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
		{
			ID:           "lease-b",
			Goal:         "grade writes in the guard",
			WritePaths:   []string{"cmd/magus/**", "docs/guard.md"},
			DenyPaths:    []string{"cmd/magus/gen/**"},
			State:        types.StateDeclared,
			Checkpoint:   "rev-a",
			ReportedBase: "rev-a",
			BaseVerdict:  types.BaseMatch,
			Registered:   1,
		},
	}
}

// narrowLease is a delegated worker assigned one package's tests: the shape the
// multi-agent skill hands out, and the shape the gate deny is scoped to. Mirrors
// internal/guard's own narrowLease, unexported there.
func narrowLease() types.Lease {
	return types.Lease{
		ID:         "harness/lease-scoped-deny",
		Goal:       "lease-scoped denies in the guard",
		WritePaths: []string{"cmd/magus/**"},
		Validation: "magus run go::go-test . -- ./internal/ledger/",
		State:      types.StateRunning,
		Registered: 1,
	}
}

// serveNext writes one journal entry in the shape the serving side appends, so the two
// sides of the contract are pinned by the same literal a reader can compare to the docs.
// Mirrors internal/guard's own serveNext, unexported there.
func serveNext(t *testing.T, gate hint.Gate, id string, argv ...string) {
	t.Helper()
	path := hint.ServedNextPath(gate.CacheDir())
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

// touchedProjects reads the marker recording which projects this session has written
// to, by the same kind string internal/guard's own touchedProjects (drift.go)
// reads ("touched-projects"); that helper is unexported to internal/guard, so this
// package mirrors its read side for the one assertion that needs it.
func touchedProjects(g hint.Gate) []string {
	if g.CacheDir() == "" {
		return nil
	}
	body, err := os.ReadFile(hint.MarkerPath(g.CacheDir(), g.Session(), "touched-projects"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// TestGuardDecisionsCoverEveryVerdictTheHookEmits keeps agent.GuardDecisions
// honest, which is what makes it usable as the contract the host-parity gate
// compares against (see TestHostGluesCoverTheGuardContract in dogfood_test.go).
//
// Two directions, and both are load-bearing:
//
//   - Every decision the hook can EMIT must be listed. A source scan rather
//     than a behavioral assertion, for the same reason
//     TestEveryCommandBindsDisplayFlags is one: catching an emitted-but-
//     unlisted decision at runtime would mean enumerating every input that
//     produces every verdict, and that enumeration is the thing that goes
//     stale.
//   - Every listed decision must RENDER distinctly. writeGuardVerdict's text
//     arm falls through to "pass" for anything it does not know, so a decision
//     added to the list but not to the renderer would report itself as a pass:
//     the quietest possible wrong answer.
//
// A decision that fails either direction is not a contract a host glue can be
// asked to declare a stance on.
func TestGuardDecisionsCoverEveryVerdictTheHookEmits(t *testing.T) {
	listed := make(map[string]bool)
	for _, d := range agent.GuardDecisions() {
		listed[d] = true
	}

	// Every guard source, not one file: the rules were split out of agent.go and a
	// hard-coded name would silently stop scanning the file the decisions moved to.
	// They now live in internal/guard, not this directory.
	sources, err := filepath.Glob("../../internal/guard/guard*.go")
	require.NoError(t, err)
	require.NotEmpty(t, sources)
	found := false
	for _, src := range sources {
		body, rerr := os.ReadFile(src)
		require.NoError(t, rerr)
		for _, m := range verdictDecisionRe.FindAllStringSubmatch(string(body), -1) {
			found = true
			assert.True(t, listed[m[1]],
				"%s emits the verdict decision %q, which agent.GuardDecisions does not list.\n"+
					"Add it there first: the host-parity gate asks every glue to declare a stance per\n"+
					"decision, and a decision missing from the contract is one no host was asked about.", src, m[1])
		}
	}
	require.True(t, found, "found no decision literals in guard*.go; verdictDecisionRe no longer matches how the guard assigns a decision")

	for _, d := range agent.GuardDecisions() {
		var out strings.Builder
		require.NoError(t, writeGuardVerdict(&out, OutputOptions{Format: FormatText}, guard.Verdict{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      d,
			Reason:        "why",
			Context:       "why",
		}))
		assert.True(t, strings.HasPrefix(out.String(), d),
			"writeGuardVerdict renders the listed decision %q as %q: its text arm has no case for it and\n"+
				"fell through to the default, so the verdict reads as a pass.", d, strings.TrimSpace(out.String()))
	}
}

// TestHookCmd covers the stdin-only guard boundary, the standard output arm,
// and the fail-open contract for empty input. Host-specific event extraction
// happens before the command is piped to magus.
func TestHookCmd(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	auditDir := t.TempDir()
	run := func(stdin string, args ...string) string {
		var out strings.Builder
		// The display flags live on a package global and default to whatever it
		// already holds, so one case passing -o json would otherwise leak into
		// every later case. Harmless in the real CLI (one command per process),
		// load-bearing here, and the reason this reset exists rather than a
		// local output flag, which is what the command used to have.
		global = globalFlags{}
		ctx := guard.WithLocation(context.Background(), auditDir, "/repo/magus", "")
		// A deny now exits non-zero, which is the whole point of the guard: the
		// rendered verdict on stdout is unchanged, and the error is the blocking
		// signal a host reads. Anything OTHER than that is a real failure.
		if err := hookCmd(ctx, strings.NewReader(stdin), &out, args); err != nil {
			var silent errSilent
			require.ErrorAs(t, err, &silent)
			require.Equal(t, guardDenyExitCode, silent.exitCode)
		}
		return out.String()
	}

	assert.True(t, strings.HasPrefix(run("git commit -m x"), "advise: "))
	assert.True(t, strings.HasPrefix(run("git stash"), "deny: "))

	got := run("git stash", "-o", "json")
	assert.Contains(t, got, `"decision": "deny"`)
	assert.Contains(t, got, `"schema_version": 1`)
	assert.Contains(t, got, "magus-vcs-hygiene")

	// A template renders a host dialect; pass renders empty, deny fills it.
	tpl := `template={{if eq .decision "deny"}}{"permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}{{end}}`
	assert.Contains(t, run("git stash", "-o", tpl), `"permissionDecision":"deny"`)
	assert.Empty(t, strings.TrimSpace(run("ls", "-o", tpl)))

	// -o name is the bare decision word.
	assert.Equal(t, "deny\n", run("git stash", "-o", "name"))

	// Fail open on empty stdin; positional input is rejected instead of quietly
	// creating a second input contract.
	assert.Equal(t, "pass\n", run(""))
	var positionalOut strings.Builder
	err := hookCmd(context.Background(), strings.NewReader(""), &positionalOut, []string{"git", "stash"})
	require.ErrorContains(t, err, "no positional arguments")
}

// TestHookCmd_UnreadableStdinFailsClosed pins the one input case that does NOT
// fail open. An empty stdin is a host that sent nothing; a stdin that ERRORS is a
// payload that was lost in flight, and answering pass there reports a command the
// guard never saw as cleared. The manpage's exit table promises the same: deny and
// unreadable input share code 2 so a host that blocks on 2 fails closed in both.
func TestHookCmd_UnreadableStdinFailsClosed(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")

	// A truncated payload, which is the shape that actually occurs: some bytes
	// arrive and the rest never does.
	truncated := func() io.Reader {
		return io.MultiReader(strings.NewReader("git sta"), iotest.ErrReader(errors.New("read |0: file already closed")))
	}

	var out bytes.Buffer
	err := hookCmd(ctx, truncated(), &out, []string{"-o", "name"})
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.exitCode)
	assert.Equal(t, "deny\n", out.String())

	// The reason names what happened and what to do about it, so a blocked agent is
	// not left guessing at a guard it cannot see.
	out.Reset()
	require.Error(t, hookCmd(ctx, truncated(), &out, []string{"-o", "json"}))
	assert.Contains(t, out.String(), "could not read its input from stdin")
	assert.Contains(t, out.String(), "file already closed")

	// --observe carries no verdict, so it keeps the documented "always exits 0".
	out.Reset()
	require.NoError(t, hookCmd(ctx, truncated(), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())
}

func TestHookCmd_AppendsNormalizedActivity(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	// git stash is denied, so hookCmd reports it by exiting non-zero while still
	// rendering the verdict in the requested format.
	err := hookCmd(ctx, strings.NewReader("git stash"), &out, []string{"-o", "name"})
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	require.Equal(t, guardDenyExitCode, silent.exitCode)
	assert.Equal(t, "deny\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, trail.KindAgentCommand, got.Kind)
	assert.Equal(t, "agent", got.Actor)
	assert.Equal(t, "/repo/magus", got.Workspace)
	assert.Equal(t, "shell.command", got.Action)
	assert.Equal(t, "guard: deny", got.Preview)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"tool":"shell.command","command":"git stash"}`, string(body))
	body, err = trail.ReadBlob(dir, got.ResponseRef)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"schema_version":1`)
	assert.Contains(t, string(body), `"decision":"deny"`)
	assert.Contains(t, string(body), `"reason":`)
}

func TestHookCmd_PathAndEmptyInputActivity(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--path", "-o", "name"}))
	assert.Equal(t, "advise\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, trail.KindAgentCommand, got.Kind)
	assert.Equal(t, "agent", got.Actor)
	assert.Equal(t, "file.write", got.Action)
	assert.Equal(t, "guard: advise", got.Preview)
	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"path":"AGENTS.md","tool":"file.write"}`, string(body))

	emptyDir := t.TempDir()
	emptyCtx := guard.WithLocation(context.Background(), emptyDir, "/repo/magus", "")
	out.Reset()
	require.NoError(t, hookCmd(emptyCtx, strings.NewReader(""), &out, nil))
	assert.Equal(t, "pass\n", out.String())
	events, err = trail.ReadRecent(emptyDir, 1)
	require.NoError(t, err)
	assert.Empty(t, events, "a hook with no command/path has no observable invocation to record")
}

// TestHookCmd_RecordsHostAttribution covers the --agent-name/--session/--event flags: the wrapper is
// the only party that knows which agent host ran the hook, so what it passes must survive onto
// the event line, not only into the request blob.
func TestHookCmd_RecordsHostAttribution(t *testing.T) {
	global = globalFlags{}
	// The whole-struct assertion below includes Lease, which the hook reads off the
	// environment, so a developer or CI job that exported one would fail this test over
	// its own baggage.
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &out,
		[]string{"--agent-name", "claude-code", "--session", "abc123", "--event", "PreToolUse", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]

	// Whole-struct assertion with the content-addressed and clock-dependent fields lifted out
	// first, so a new field on Event cannot be silently dropped by the hook producer.
	requestRef, responseRef := got.RequestRef, got.ResponseRef
	got.Ts, got.RequestRef, got.ResponseRef = 0, "", ""
	got.RequestBytes, got.ResponseBytes = 0, 0
	assert.Equal(t, trail.Event{
		Kind:      trail.KindAgentCommand,
		Actor:     "agent",
		Host:      "claude-code",
		Session:   "abc123",
		Workspace: "/repo/magus",
		Action:    "shell.command",
		Outcome:   trail.OutcomeOK,
		Preview:   "guard: pass",
	}, got)

	body, err := trail.ReadBlob(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"abc123","event":"PreToolUse","tool":"shell.command","command":"ls"}`, string(body))
	body, err = trail.ReadBlob(dir, responseRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"decision":"pass"}`, string(body))
}

// TestHookCmd_AttributionIsOptional holds the fail-open contract: attribution is best-effort
// metadata, so a wrapper that supplies none still gets a verdict and still records an event.
func TestHookCmd_AttributionIsOptional(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Empty(t, got.Host)
	assert.Empty(t, got.Session)

	// Omitted rather than recorded empty: the blob says nothing was known, not that the host is "".
	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"tool":"shell.command","command":"ls"}`, string(body))
}

// TestHookCmd_ObserveRecordsWithoutJudging pins what --observe is for. AGENTS.md is the exact
// path TestHookCmd_PathAndEmptyInputActivity gets an ADVISE for as a write, so a pass here is
// specifically the observation being exempted from the write rules rather than the rule
// failing to fire. Without it, a hook wired to a host's read tool would advise "you are
// editing a declared output" at a file the agent only opened.
func TestHookCmd_ObserveRecordsWithoutJudging(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "file.read", got.Action, "a reach is recorded under its own label, not as a write")

	// "observed", not "guard: pass". The trail already distinguishes the two, and recording
	// a verdict here would have every read claim the guard ran and cleared it: the exact
	// conflation --observe exists to remove. The wire verdict the host reads is still pass.
	assert.Equal(t, "observed", got.Preview)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"tool":"file.read","path":"AGENTS.md"}`, string(body))
	body, err = trail.ReadBlob(dir, got.ResponseRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"decision":""}`, string(body))
}

// TestHookCmd_ObserveOutranksPath holds the precedence the wrapper depends on: a host whose
// read event carries a file_path will send --observe alongside the envelope that sets --path,
// and the observation must win. The reverse would silently restore the false advisory.
func TestHookCmd_ObserveOutranksPath(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--path", "--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "file.read", events[0].Action)
}

// TestHookCmd_RecordsTranscriptPath covers the session-to-transcript link. The id groups a
// session's events; the path is what a reader follows to see the rest. magus records the
// POINTER and never opens the file, which is what keeps the trail paths-and-timings.
func TestHookCmd_RecordsTranscriptPath(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	envelope := `{"hook_event_name":"PreToolUse","session_id":"s1","transcript_path":"/tmp/t.jsonl","tool_input":{"command":"ls"}}`
	require.NoError(t, hookCmd(ctx, strings.NewReader(envelope), &out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "s1", got.Session)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"session":"s1","transcript":"/tmp/t.jsonl","event":"PreToolUse","tool":"shell.command","command":"ls"}`, string(body))
}

// TestHookCmd_TranscriptFlagRecordsThePointer covers the FLAG path, which is the one the
// shipped observe template actually uses. That template extracts the path with jq and pipes
// plain text rather than the whole event, so nothing about the envelope is available to it;
// without the flag the transcript link exists only for hosts that pipe raw JSON, which is
// none of the ones magus ships a template for.
func TestHookCmd_TranscriptFlagRecordsThePointer(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("internal/trail/trail.go"), &out,
		[]string{"--observe", "--agent-name", "claude-code", "--session", "s9", "--transcript", "/tmp/t.jsonl", "--event", "PreToolUse", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "file.read", got.Action)
	assert.Equal(t, "s9", got.Session)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"s9","transcript":"/tmp/t.jsonl","event":"PreToolUse","tool":"file.read","path":"internal/trail/trail.go"}`, string(body))
}

// TestHookCmd_ObserveWithNoInputRecordsNothing: a wrapper whose host event carried no path
// has nothing to report, and an observation with no subject is dropped like any other empty
// one rather than being invented as ".", which would claim a reach the host never described.
func TestHookCmd_ObserveWithNoInputRecordsNothing(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(""), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	assert.Empty(t, events)
}

// TestHookCmd_RecordsSpawnFromEnvelope covers the spawn surface end to end: a host payload
// carrying a prompt rather than a command is recorded as a spawn, with the handed context in the
// blob and the cooperative lease marker stamped onto the event.
//
// It also pins the thing that must NOT happen. The prompt below quotes `git stash`, which the
// command guard denies. A spawn is not a guard surface, so the verdict is a pass and the
// spawn is recorded rather than blocked for describing a denied command.
func TestHookCmd_RecordsSpawnFromEnvelope(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")
	envelope := `{"hook_event_name":"PreToolUse","session_id":"abc123","tool_name":"Task",` +
		`"tool_input":{"description":"audit the store","subagent_type":"Explore",` +
		`"prompt":"lease: notes-store-6b\nDo not run git stash anywhere."}}`

	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(envelope), &out,
		[]string{"--agent-name", "claude-code", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]

	requestRef := got.RequestRef
	got.Ts, got.RequestRef, got.RequestBytes = 0, "", 0
	assert.Equal(t, trail.Event{
		Kind:      trail.KindAgentSpawn,
		Actor:     "agent",
		Host:      "claude-code",
		Session:   "abc123",
		Workspace: "/repo/magus",
		Action:    "Explore",
		Lease:     "notes-store-6b",
		Outcome:   trail.OutcomeOK,
	}, got)

	body, err := trail.ReadBlob(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"abc123","event":"PreToolUse",`+
		`"tool":"Task","child":"Explore","lease":"notes-store-6b",`+
		`"context":"lease: notes-store-6b\nDo not run git stash anywhere."}`, string(body))
}

// TestHookCmd_SpawnWithoutMarkerOrLabel holds the two halves of the cooperative contract: an
// orchestrator that writes no marker still gets an audited spawn record, just an uncorrelated one,
// and a host whose payload names no callee still records a spawn.
func TestHookCmd_SpawnWithoutMarkerOrLabel(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	dir := t.TempDir()
	ctx := guard.WithLocation(context.Background(), dir, "/repo/magus", "")

	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(`{"tool_input":{"prompt":"go and audit the store"}}`),
		&out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, trail.KindAgentSpawn, events[0].Kind)
	assert.Equal(t, "agent.spawn", events[0].Action)
	assert.Empty(t, events[0].Lease)
}

// TestHookPathMode covers --path, the definitive (non-heuristic) arm: a
// declared target output is denied. The deny path needs a real workspace, so it
// is exercised end to end elsewhere; what matters here is that the mode parses,
// shares the standard output arm, and FAILS OPEN on anything it cannot classify.
func TestHookPathMode(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "unclassifiable path", args: []string{"--path", "-o", "name"}},
		{name: "empty path", args: []string{"--path", "-o", "name"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			ctx := guard.WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")
			input := ""
			if tt.name == "unclassifiable path" {
				input = "/nonexistent/elsewhere.txt"
			}
			require.NoError(t, hookCmd(ctx, strings.NewReader(input), &out, tt.args))
			assert.Equal(t, "pass\n", out.String(),
				"an unclassifiable path says nothing: an advisory fired on a guess trains the reader to ignore it")
		})
	}
}

// TestHookCmd_EnvelopeWithNothingToJudge pins the arm that produced false denies: a host
// envelope for a tool the guard has no rule for (a todo list, a search) carries no
// command, path or prompt, and used to fall through to being judged as the literal JSON.
// The shell rules then read a denied command QUOTED inside the payload as the command
// about to run, so writing a todo that says not to run `sed -i` was itself blocked.
//
// The decoder's own half of this (decodeHookEnvelope, unexported to internal/guard) is
// pinned by TestDecodeHookEnvelope in internal/guard/guard_test.go; this covers only the
// end-to-end hookCmd path.
func TestHookCmd_EnvelopeWithNothingToJudge(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	const payload = `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"TodoWrite",` +
		`"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`

	global = globalFlags{}
	var out bytes.Buffer
	ctx := guard.WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")
	require.NoError(t, hookCmd(ctx, strings.NewReader(payload), &out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())
}

// TestEnforceVerdictBlocksOnlyDeny pins the half that makes the guard real. Every rule was
// reachable and correct while the process exited 0, so a host read success and ran the
// command anyway: the guard looked installed and enforced nothing.
func TestEnforceVerdictBlocksOnlyDeny(t *testing.T) {
	text := OutputOptions{Format: FormatText}
	err := enforceVerdict(text, guard.Verdict{Decision: "deny", Reason: "no"})
	require.Error(t, err, "a deny must exit non-zero or it blocks nothing")
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.exitCode)

	assert.NoError(t, enforceVerdict(text, guard.Verdict{Decision: "advise", Context: "fyi"}),
		"advice teaches and must never block")
	assert.NoError(t, enforceVerdict(text, guard.Verdict{Decision: "pass"}))

	// The exit code is the enforcement and does not depend on the rendering: a structured
	// consumer that got a zero status would be told the same lie in a different shape.
	require.Error(t, enforceVerdict(OutputOptions{Format: FormatJSON}, guard.Verdict{Decision: "deny", Reason: "no"}))
}

// TestGuardDenyPrintsItsReasonOnce: text mode already renders the full reason to stdout, and
// every guard template this repo ships reads the verdict from stdout (one discards stderr
// outright), so an unconditional stderr copy reached nobody who lacked another channel and
// simply printed a kilobyte-plus reason twice to a terminal. A structured format renders no
// prose, so there stderr is the only readable channel and keeps it.
func TestGuardDenyPrintsItsReasonOnce(t *testing.T) {
	deny := guard.Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "deny", Reason: "because"}

	var stdout bytes.Buffer
	require.NoError(t, writeGuardVerdict(&stdout, OutputOptions{Format: FormatText}, deny))
	assert.Equal(t, 1, strings.Count(stdout.String(), "because"), "stdout renders the reason once")
	assert.Empty(t, captureStderr(t, func() {
		_ = enforceVerdict(OutputOptions{Format: FormatText}, deny)
	}), "and stderr must not repeat what stdout just said")

	assert.Contains(t, captureStderr(t, func() {
		_ = enforceVerdict(OutputOptions{Format: FormatJSON}, deny)
	}), "because", "a structured format renders no prose, so stderr carries it")
}

// TestReadGuardInputRefusesAnOversizePayload: a truncated payload is exactly the "not the
// command the guard was shown" case, so the overflow is a read failure and hookCmd's
// deny arm turns it into a block.
func TestReadGuardInputRefusesAnOversizePayload(t *testing.T) {
	value, err := readGuardInput(strings.NewReader(strings.Repeat("x", guardInputLimit)))
	require.NoError(t, err)
	assert.Len(t, value, guardInputLimit)

	_, err = readGuardInput(strings.NewReader(strings.Repeat("x", guardInputLimit+1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not read whole")
}

// TestHookCmdAdvisesOncePerSession is the same rule through the command the host actually
// runs. The graph-beats-grep hint is the measured case: it fired dozens of times in one
// session with byte-identical text.
func TestHookCmdAdvisesOncePerSession(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	base, root := t.TempDir(), t.TempDir()
	ctx := guard.WithLocation(t.Context(), base, root, "")
	run := func(session string) string {
		var out strings.Builder
		// The display flags live on a package global, so one case must not leak into the
		// next; TestHookCmd resets the same way and for the same reason.
		global = globalFlags{}
		require.NoError(t, hookCmd(ctx, strings.NewReader("rg needle"), &out, []string{"--session", session}))
		return out.String()
	}

	first := run("session-1")
	require.True(t, strings.HasPrefix(first, "advise: "))
	assert.Contains(t, first, "knowledge graph")

	repeat := run("session-1")
	assert.NotContains(t, repeat, "knowledge graph", "the repeat drops the full text")
	assert.Contains(t, repeat, "magus refs", "the repeat still names the command, which is what converts")
	assert.Less(t, len(repeat), len(first)/4, "a repeat nobody has to read around")

	assert.Contains(t, run("session-2"), "knowledge graph", "a fresh session is owed the fact once")
}

// TestHookCmdScopesSearchAdviceFromManifest pins the wiring from the knowledge
// manifest to the advisory text: a search pointed at a project directory gets a
// project=-scoped query suggestion, and a workspace with no manifest gets the
// unscoped advice it always got.
func TestHookCmdScopesSearchAdviceFromManifest(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	run := func(t *testing.T, base, command string) string {
		t.Helper()
		ctx := guard.WithLocation(t.Context(), base, t.TempDir(), "")
		var out strings.Builder
		global = globalFlags{}
		require.NoError(t, hookCmd(ctx, strings.NewReader(command), &out, []string{"--session", "session-1"}))
		return out.String()
	}

	t.Run("manifest projects scope the suggestion", func(t *testing.T) {
		base := t.TempDir()
		man := fmt.Sprintf(`{"schema_version":%d,"shards":{"docs":{},".":{},"@runtime":{},"docs@symbols":{}}}`,
			types.KnowledgeSchemaVersion)
		require.NoError(t, os.MkdirAll(filepath.Join(base, "knowledge"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(base, "knowledge", "manifest.json"), []byte(man), 0o644))

		got := run(t, base, "grep -rn Foo docs/")
		require.True(t, strings.HasPrefix(got, "advise: "))
		assert.Contains(t, got, `magus query Foo 'project=~^docs(/|$)'`)
	})

	t.Run("no manifest stays unscoped", func(t *testing.T) {
		got := run(t, t.TempDir(), "grep -rn Foo docs/")
		require.True(t, strings.HasPrefix(got, "advise: "))
		// The closing backtick is what carries the assertion: it proves no matcher
		// follows the pattern. The generic reason below the lead documents the
		// `project=<p>` grammar in prose, so a bare `project=` is present either way
		// and asserting its absence could never fail.
		assert.Contains(t, got, "`magus query Foo` - ")
		assert.NotContains(t, got, "project=~", "only the scoped path emits a regex project matcher")
	})
}

// TestHookCmdRepeatsEveryDenial is the exemption, and it is the more important half. A
// refusal explains itself every time it refuses: it is the one verdict the caller cannot
// see past, and a second identical `git stash` blocked with no reason is a dead end.
func TestHookCmdRepeatsEveryDenial(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	base, root := t.TempDir(), t.TempDir()
	ctx := guard.WithLocation(t.Context(), base, root, "")
	run := func() string {
		var out strings.Builder
		global = globalFlags{}
		err := hookCmd(ctx, strings.NewReader("git stash"), &out, []string{"--session", "session-1"})
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
		return out.String()
	}
	assert.Equal(t, run(), run(), "a denial repeats its reason verbatim, however many times it fires")
}

// TestHookCmdRoutesAnAgentSurfaceWrite pins the WIRING, not the rule: a rule that is
// correct and never called is the failure mode this repository has shipped before. It also
// pins the dedupe on the path surface, where a suppressed advisory must leave silence
// rather than let the next rung speak into it.
func TestHookCmdRoutesAnAgentSurfaceWrite(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "magus"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755))

	ctx := guard.WithLocation(t.Context(), t.TempDir(), root, "")
	run := func() string {
		var out strings.Builder
		global = globalFlags{}
		require.NoError(t, hookCmd(ctx, strings.NewReader("internal/agent/skills/magus-run/SKILL.md"),
			&out, []string{"--path", "--session", "session-1"}))
		return out.String()
	}

	assert.Contains(t, run(), "magus-skill-authoring")
	assert.Equal(t, "pass\n", run(),
		"the repeat is silence, not the new-directory advisory stepping into the gap this rule left")
}

// TestHookCmdJudgesTheCacheDirOnBothSurfaces is the hook's decision table for this rule.
// It is here rather than in TestEvaluateBashGuard because the rule is not pure: it reads
// the resolved cache location, so the table it belongs in is the one that runs hookCmd.
//
// The rows run UNBOUND, which is the contract: this is the guard's own evidence rather
// than a lane, so an orchestrator and a person in their own checkout are refused too.
func TestHookCmdJudgesTheCacheDirOnBothSurfaces(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		path  bool
		want  string
	}{
		"the lease marker":    {input: ".magus/lease", path: true, want: "deny\n"},
		"a served-next entry": {input: ".magus/advisories/anon.served-next", path: true, want: "deny\n"},
		// Spelled bare, so it resolves inside this package's own directory: a path
		// naming a directory that does not exist yet draws the new-directory advisory,
		// which is a different rule answering and would say nothing about this one.
		"a source file":           {input: "guard_cachedir.go", path: true, want: "pass\n"},
		"a sibling directory":     {input: ".magus-notes/a.md", path: true, want: "pass\n"},
		"appending to a journal":  {input: "echo x >> .magus/advisories/anon.served-next", want: "deny\n"},
		"removing the whole dir":  {input: "rm -rf .magus", want: "deny\n"},
		"an in-place marker edit": {input: "sed -i 's/a/b/' .magus/lease", want: "deny\n"},
		"copying over the marker": {input: "cp foo .magus/lease", want: "deny\n"},
		"reading a log":           {input: "cat .magus/logs/x.log", want: "pass\n"},
		"reading a captured run":  {input: "./magus query output outabc", want: "pass\n"},
		"binding through magus":   {input: "./magus session lease adj/x", want: "pass\n"},
		"a sibling name":          {input: "echo x >> .magus-notes/a.md", want: "pass\n"},
	} {
		t.Run(name, func(t *testing.T) {
			global = globalFlags{}
			t.Setenv(trail.EnvBaggage, "")
			ctx, root, _ := fleetFixture(t)
			// The fixture's cache dir is already elsewhere, so these rows exercise the
			// literal spelling against a RELOCATED dir, which is the harder half.
			require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))

			args := []string{"-o", "name"}
			if tc.path {
				args = append(args, "--path")
			}
			var out bytes.Buffer
			err := hookCmd(ctx, strings.NewReader(tc.input), &out, args)
			if tc.want == "deny\n" {
				require.Error(t, err, "a deny that exits 0 blocks nothing")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, out.String())
		})
	}
}

// TestHookCmdDeniesTheCacheDirAheadOfTheLaneItSitsIn is the rank the rule was written for:
// a worker handed a lane that covers the dir must read what the dir IS, not a verdict
// about whose lane it is.
func TestHookCmdDeniesTheCacheDirAheadOfTheLaneItSitsIn(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	lease := narrowLease()
	lease.WritePaths = []string{"**"}
	// Unregistered, so the lane rule has a denial of its own to be outranked BY. A lane
	// that covers the path and says nothing leaves the rank unobserved.
	lease.Registered = 0
	ctx, _, _ := fleetFixture(t, lease)

	var out bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(".magus/lease"), &out,
		[]string{"--path", "--lease", lease.ID, "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "magus cache dir")
	assert.Contains(t, out.String(), "magus is the only writer of it")
	assert.NotContains(t, out.String(), "registered the base it landed on",
		"the lane rules must not answer for this path")
}

// The wiring, end to end through the hook: a write that reaches a verdict leaves the
// project it landed in recorded for the session, whatever that verdict was. Nothing
// below this line fires the advisory, and that is the point: the set has to be filled
// by every write, not only by the ones this rule speaks about.
//
// The workspace comes from the same memoized load the rule itself uses, rather than
// from a root this test resolves on its own. That load is a package-level sync.Once, so
// whichever test in this binary reaches it first pins it; asserting against a root this
// test picked would pass or fail on test ORDER, since the rule is silent whenever the
// loaded workspace is not the one the host reported.
func TestHookCmdRecordsTheProjectAWriteTouched(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	ws, err := inspectWorkspace(t.Context(), "")
	require.NoError(t, err)

	base := t.TempDir()
	ctx := guard.WithLocation(t.Context(), base, ws.Root(), "")
	global = globalFlags{}
	var out strings.Builder
	require.NoError(t, hookCmd(ctx, strings.NewReader(filepath.Join(ws.Root(), "drift-fixture.txt")),
		&out, []string{"--path", "--session", "session-1"}))

	assert.Equal(t, []string{"."}, touchedProjects(hint.NewGate(base, "session-1")),
		"a workspace-root file belongs to the root project, whatever else this workspace declares")
}

// TestHookCmdDeniesTheGateUnderANarrowLease proves the WIRING. The rule itself is covered
// above; what this pins is that hookCmd reaches it, because a rule nothing calls never
// fires however well it is tested.
func TestHookCmdDeniesTheGateUnderANarrowLease(t *testing.T) {
	global = globalFlags{}
	// The hook falls back to the environment for the lease, so a developer or CI job that
	// exported one would decide the control case below.
	t.Setenv(trail.EnvBaggage, "")
	ctx, _, _ := fleetFixture(t, narrowLease())
	command := "./magus affected ci --no-default-charms"

	var denied bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(command), &denied,
		[]string{"--lease", "harness/lease-scoped-deny", "-o", "name"})
	require.Error(t, err, "a deny that exits 0 blocks nothing: the host runs the command anyway")
	assert.Equal(t, "deny\n", denied.String())

	var unleased bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(command), &unleased, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", unleased.String(), "a caller naming no lease is scoped by nobody's row")
}

// TestHookEnvelopeCwdLocatesTheWorkersCheckout pins the channel a host that runs its hooks
// somewhere else reaches the worker's marker through: the envelope's cwd names the worker's
// checkout, and the lease bound there scopes the verdict, whatever the hook process's own
// directory is.
func TestHookEnvelopeCwdLocatesTheWorkersCheckout(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	worker := narrowLease()
	worker.Parent = "harness"
	_, err = ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}).
		Update(t.Context(), worker.ID, func(cur *types.Lease) { *cur = worker })
	require.NoError(t, err)
	require.NoError(t, ledger.BindLease(cacheDir, worker.ID))

	envelope := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":%q,"tool_input":{"command":"git commit -m done"}}`, root)
	var out bytes.Buffer
	err = hookCmd(context.Background(), strings.NewReader(envelope), &out, []string{"-o", "name"})
	var silent errSilent
	require.ErrorAs(t, err, &silent, "the worker lease bound in the envelope's checkout must deny the commit")
	assert.Equal(t, "deny\n", out.String())

	events, err := trail.ReadRecent(cacheDir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1, "the observation lands in the worker's trail, not the hook's cwd")
	assert.Equal(t, worker.ID, events[0].Lease)
}

// TestHookCmdJudgesTheMCPLedgerSurface is the decision table for the transport the CLI
// rules would otherwise miss: the same envelope a host forwards for an MCP tool call.
func TestHookCmdJudgesTheMCPLedgerSurface(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	wide := narrowLease()
	wide.WritePaths = []string{"cmd/magus/**", "internal/hint/**"}
	ctx, _, _ := fleetFixture(t, wide)

	for name, tc := range map[string]struct {
		toolInput string
		want      string
	}{
		"put on another row":    {`{"op":"put","id":"harness/other","write_paths":"**"}`, "deny\n"},
		"put widening its own":  {`{"op":"put","id":"` + wide.ID + `","write_paths":["**"]}`, "deny\n"},
		"clearing the board":    {`{"op":"clear"}`, "deny\n"},
		"register elsewhere":    {`{"op":"register","id":"harness/other"}`, "deny\n"},
		"register its own base": {`{"op":"register","id":"` + wide.ID + `","reported_base":"abc123"}`, "pass\n"},
		"listing the plan":      {`{"op":"list"}`, "pass\n"},
		"giving a lane back":    {`{"op":"put","id":"` + wide.ID + `","write_paths":["cmd/magus/**"]}`, "pass\n"},
		// The rewrite the rendered line used to drop on the floor: judged only on the keys
		// the renderer carried, a shrink beside a forged checkpoint read as a plain shrink.
		"forging its own base": {`{"op":"put","id":"` + wide.ID + `","write_paths":["cmd/magus/**"],"checkpoint":"deadbeef"}`, "deny\n"},
		"rewriting its goal":   {`{"op":"put","id":"` + wide.ID + `","write_paths":["cmd/magus/**"],"goal":"something else"}`, "deny\n"},
	} {
		envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-` + name +
			`","tool_name":"mcp__magus__magus_ledger","tool_input":` + tc.toolInput + `}`
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(envelope), &out, []string{"--lease", wide.ID, "-o", "name"})
		if tc.want == "deny\n" {
			require.Error(t, err, name)
		} else {
			require.NoError(t, err, name)
		}
		assert.Equal(t, tc.want, out.String(), name)
	}
}

// TestHookCmdErrorsOnAnUndeclaredLease is C3: an id nobody declared bought SILENT
// un-enrolled treatment, so a worker whose orchestrator typo'd the id ran unguarded and
// looked exactly like a guarded one.
func TestHookCmdErrorsOnAnUndeclaredLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _, _ := fleetFixture(t, narrowLease())

	var out bytes.Buffer
	err := hookCmd(ctx, strings.NewReader("ls"), &out, []string{"--lease", "harness/typo", "-o", "json"})
	var silent errSilent
	require.ErrorAs(t, err, &silent, "an assertion that does not resolve must not exit 0")
	assert.Equal(t, guardDenyExitCode, silent.exitCode)
	assert.Contains(t, out.String(), `"decision": "deny"`)
	assert.Contains(t, out.String(), "is not declared")
	assert.Contains(t, out.String(), `"lease": "harness/typo"`, "the verdict names the row it graded against")
}

// TestHookCmdNoticesATerminalLease covers the other half: a row that has finished still
// names a session, and every rule keyed on it has quietly stopped applying.
func TestHookCmdNoticesATerminalLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	done := narrowLease()
	done.State = types.StatePass
	ctx, _, _ := fleetFixture(t, done)

	var first bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &first,
		[]string{"--lease", done.ID, "--session", "session-terminal"}))
	assert.Contains(t, first.String(), "is in state pass")
	assert.Contains(t, first.String(), "its rules are inert")

	var second bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &second,
		[]string{"--lease", done.ID, "--session", "session-terminal"}))
	assert.Equal(t, "pass\n", second.String(), "a standing fact is said once per session")
}

// TestHookVerdictCarriesTheActingLease pins the field on the wire, which is what lets a
// person see WHICH row decided a verdict.
func TestHookVerdictCarriesTheActingLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _, _ := fleetFixture(t, narrowLease())

	var leased bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &leased,
		[]string{"--lease", narrowLease().ID, "-o", "json"}))
	assert.Contains(t, leased.String(), `"lease": "harness/lease-scoped-deny"`)

	var unbound bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &unbound, []string{"-o", "json"}))
	assert.NotContains(t, unbound.String(), `"lease"`, "a session nobody leased names no row")
}

// TestHookCmdAdvisesAnInvalidLeaseOnEverySurface: the notice lived inside gradeLeasedWrite,
// which runs on the path surface only, so a COMMAND under a typo'd id ran fully un-enrolled
// with nothing said about it.
func TestHookCmdAdvisesAnInvalidLeaseOnEverySurface(t *testing.T) {
	for name, args := range map[string][]string{
		"the command surface": {"--lease", "has spaces", "--session", "invalid-command", "-o", "json"},
		"the path surface":    {"--path", "--lease", "has spaces", "--session", "invalid-path", "-o", "json"},
	} {
		t.Run(name, func(t *testing.T) {
			global = globalFlags{}
			t.Setenv(trail.EnvBaggage, "")
			ctx, _, _ := fleetFixture(t, narrowLease())

			var out bytes.Buffer
			require.NoError(t, hookCmd(ctx, strings.NewReader("README.md"), &out, args))
			assert.Contains(t, out.String(), "is not a valid lease id")
		})
	}
}

// TestHookCmdRanksTheCacheDirAboveTheUndeclaredLease: the undeclared refusal used to return
// from hookCmd before anything else ran, so it outranked the cache-dir rule against both
// files' stated order and skipped the stale-binary notice at the tail.
func TestHookCmdRanksTheCacheDirAboveTheUndeclaredLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _, _ := fleetFixture(t, narrowLease())

	var out bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(".magus/lease"), &out,
		[]string{"--path", "--lease", "harness/nobody-declared-this", "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "magus cache dir")
	assert.NotContains(t, out.String(), "is not declared",
		"the cache dir is what the write is about; whose lane it is comes second")
}

// TestHookCmdDeniesTheGateThroughTheMCPDoor is the hole the tool-name decode closes: the
// same work the lease-scoped gate rule refuses on the command surface, asked for through
// the tool that does it. It passed unjudged while the coverage line said deny=model.
func TestHookCmdDeniesTheGateThroughTheMCPDoor(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	lease := narrowLease()
	ctx, _, _ := fleetFixture(t, lease)

	for name, toolCall := range map[string]string{
		"the affected gate": `"tool_name":"mcp__magus__magus_run_affected","tool_input":{"target":"ci"}`,
		"the gate target":   `"tool_name":"mcp__magus__magus_run_target","tool_input":{"target":"ci","projects":"."}`,
	} {
		envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-gate","` + toolCall[1:] + `}`
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(envelope), &out, []string{"--lease", lease.ID, "-o", "name"})
		require.Error(t, err, name)
		assert.Equal(t, "deny\n", out.String(), name)
	}

	// The report about the gate is not a run of it, so it still passes.
	envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-plan",` +
		`"tool_name":"mcp__magus__magus_affected_plan","tool_input":{"target":"ci"}}`
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(envelope), &out, []string{"--lease", lease.ID, "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())
}

// TestHookCmdStandsDownOnAServedNext is the rule in place: the same command is denied by a
// role-scoped rule and cleared once magus is the one that suggested it.
func TestHookCmdStandsDownOnAServedNext(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _, cacheDir := fleetFixture(t, narrowLease())
	gate := hint.NewGate(cacheDir, "session-preauth")
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
	ctx, _, cacheDir := fleetFixture(t)
	gate := hint.NewGate(cacheDir, "session-carveout")

	for _, command := range []string{"git stash", "magus affected ci | tail -5", "go test ./..."} {
		serveNext(t, gate, "fabricated", strings.Fields(command)...)
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(command), &out,
			[]string{"--session", "session-carveout", "-o", "name"})
		require.Error(t, err, "%q", command)
		assert.Equal(t, "deny\n", out.String(), "%q", command)
	}
}

// TestHookCmdDeniesAWiringWriteUnderALease proves the WIRING, the way the gate rule's own
// test does: a rule nothing calls never fires however well it is tested. It also pins the
// rank, since the lease ledger speaks first on this surface and a lane that happens to
// contain the file must not clear it.
func TestHookCmdDeniesAWiringWriteUnderALease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	lease := narrowLease()
	lease.WritePaths = []string{".claude/**", "cmd/magus/**"}
	ctx, _, _ := fleetFixture(t, lease)

	var denied bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(".claude/settings.json"), &denied,
		[]string{"--path", "--lease", lease.ID, "-o", "name"})
	require.Error(t, err, "a deny that exits 0 blocks nothing")
	assert.Equal(t, "deny\n", denied.String())

	var unbound bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(".claude/settings.json"), &unbound,
		[]string{"--path", "-o", "name"}))
	assert.Equal(t, "advise\n", unbound.String(), "an unbound session rewires its own hosts")
}

// TestHookCmdGradesAgainstTheLedger drives the wire contract rather than the grader: the
// flag reaches the rule, a denial exits with the blocking status, and the flag outranks
// the environment.
func TestHookCmdGradesAgainstTheLedger(t *testing.T) {
	ctx, root, _ := fleetFixture(t, fleetLeases()...)
	run := func(stdin string, args ...string) (string, error) {
		global = globalFlags{}
		var out strings.Builder
		err := hookCmd(ctx, strings.NewReader(stdin), &out, args)
		return out.String(), err
	}
	owned := filepath.Join(root, "internal/ledger/store.go")

	t.Run("--lease denies a write into another lease's paths", func(t *testing.T) {
		got, err := run(owned, "--path", "--lease", "lease-b", "-o", "name")
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
		assert.Equal(t, "deny\n", got)
	})

	t.Run("the baggage member supplies the default", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "userId=alice,"+trail.BaggageLease+"=lease-b")
		_, err := run(owned, "--path", "-o", "name")
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
	})

	// Baggage a magus member does not appear in is a fleet nobody enrolled: another tenant's
	// members must never be read as a lease.
	t.Run("baggage carrying no magus member enrolls nobody", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "userId=alice,serverNode=DF28")
		_, err := run(owned, "--path", "-o", "name")
		require.NoError(t, err, "an un-enrolled writer is advised, never blocked")
	})

	t.Run("the flag wins over the environment", func(t *testing.T) {
		// The path belongs to lease-a. Acting AS lease-a it is the writer's own ground,
		// so a pass here proves the flag replaced the environment's lease-b rather than
		// joining it.
		t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-b")
		_, err := run(owned, "--path", "--lease", "lease-a", "-o", "name")
		require.NoError(t, err, "acting as the owner must not be denied")
	})
}
