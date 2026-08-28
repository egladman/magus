package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verdictDecisionRe finds every decision literal the guard assigns, in either
// spelling the code uses: the struct-literal `Decision: "pass"` and the
// assignment `verdict.Decision = "deny"`.
var verdictDecisionRe = regexp.MustCompile(`Decision(?::|\s*=)\s*"(\w+)"`)

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
//     added to the list but not to the renderer would report itself as a pass -
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
	sources, err := filepath.Glob("guard*.go")
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
		require.NoError(t, writeGuardVerdict(&out, OutputOptions{Format: FormatText}, guardVerdict{
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
	auditDir := t.TempDir()
	run := func(stdin string, args ...string) string {
		var out strings.Builder
		// The display flags live on a package global and default to whatever it
		// already holds, so one case passing -o json would otherwise leak into
		// every later case. Harmless in the real CLI (one command per process),
		// load-bearing here - and the reason this reset exists rather than a
		// local output flag, which is what the command used to have.
		global = globalFlags{}
		ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: auditDir, workspace: "/repo/magus"})
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
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})

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
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
	emptyCtx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: emptyDir, workspace: "/repo/magus"})
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
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "file.read", got.Action, "a reach is recorded under its own label, not as a write")

	// "observed", not "guard: pass". The trail already distinguishes the two, and recording
	// a verdict here would have every read claim the guard ran and cleared it - the exact
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
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
// plain text rather than the whole event, so nothing about the envelope is available to it -
// without the flag the transcript link exists only for hosts that pipe raw JSON, which is
// none of the ones magus ships a template for.
func TestHookCmd_TranscriptFlagRecordsThePointer(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
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
// one rather than being invented as "." - which would claim a reach the host never described.
func TestHookCmd_ObserveWithNoInputRecordsNothing(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(""), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	assert.Empty(t, events)
}

// TestHookCmd_RecordsSpawnFromEnvelope covers the delegation surface end to end: a host payload
// carrying a prompt rather than a command is recorded as a spawn, with the handed context in the
// blob and the cooperative delegation marker stamped onto the event.
//
// It also pins the thing that must NOT happen. The prompt below quotes `git stash`, which the
// command guard denies. A spawn is not a guard surface, so the verdict is a pass and the
// delegation is recorded rather than blocked for describing a denied command.
func TestHookCmd_RecordsSpawnFromEnvelope(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	envelope := `{"hook_event_name":"PreToolUse","session_id":"abc123","tool_name":"Task",` +
		`"tool_input":{"description":"audit the store","subagent_type":"Explore",` +
		`"prompt":"delegation: notes-store-6b\nDo not run git stash anywhere."}}`

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
		Kind:       trail.KindAgentSpawn,
		Actor:      "agent",
		Host:       "claude-code",
		Session:    "abc123",
		Workspace:  "/repo/magus",
		Action:     "Explore",
		Delegation: "notes-store-6b",
		Outcome:    trail.OutcomeOK,
	}, got)

	body, err := trail.ReadBlob(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"abc123","event":"PreToolUse",`+
		`"tool":"Task","child":"Explore","delegation":"notes-store-6b",`+
		`"context":"delegation: notes-store-6b\nDo not run git stash anywhere."}`, string(body))
}

// TestHookCmd_SpawnWithoutMarkerOrLabel holds the two halves of the cooperative contract: an
// orchestrator that writes no marker still gets an audited handoff, just an uncorrelated one,
// and a host whose payload names no callee still records a spawn.
func TestHookCmd_SpawnWithoutMarkerOrLabel(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})

	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(`{"tool_input":{"prompt":"go and audit the store"}}`),
		&out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, trail.KindAgentSpawn, events[0].Kind)
	assert.Equal(t, "agent.spawn", events[0].Action)
	assert.Empty(t, events[0].Delegation)
}

// TestDecodeHookEnvelope_CommandStillWinsOverPrompt guards the ordering that makes the spawn
// branch purely additive: a payload carrying both is judged as the command it carries, so no
// envelope the guard already evaluated can start skipping the guard because a prompt appeared
// beside it.
func TestDecodeHookEnvelope_CommandStillWinsOverPrompt(t *testing.T) {
	req, ok := decodeHookEnvelope(`{"tool_input":{"command":"git stash","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "git stash", req.Value)
	assert.False(t, req.IsSpawn)

	req, ok = decodeHookEnvelope(`{"tool_input":{"file_path":"MAGUS.md","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath)
	assert.False(t, req.IsSpawn)
}

// TestHookPathMode covers --path, the definitive (non-heuristic) arm: a
// declared target output is denied. The deny path needs a real workspace, so it
// is exercised end to end elsewhere; what matters here is that the mode parses,
// shares the standard output arm, and FAILS OPEN on anything it cannot classify.
func TestHookPathMode(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "unclassifiable path", args: []string{"--path", "-o", "name"}},
		{name: "empty path", args: []string{"--path", "-o", "name"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: t.TempDir(), workspace: "/repo/magus"})
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

// TestDecodeHookEnvelope pins reading a host's hook payload directly. Without it, wiring
// the guard means `jq -r .tool_input.command | magus session hook` - an extra dependency on the
// critical path of every tool call, in the one place that must not fail.
func TestDecodeHookEnvelope(t *testing.T) {
	cmdPayload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash",` +
		`"tool_input":{"command":"magus run ci | tail"}}`
	req, ok := decodeHookEnvelope(cmdPayload)
	require.True(t, ok)
	assert.Equal(t, "magus run ci | tail", req.Value)
	assert.False(t, req.IsPath)
	assert.Equal(t, "s1", req.Who.Session)
	assert.Equal(t, "PreToolUse", req.Who.Event)

	// A file_path payload is a WRITE, so the envelope decides the --path question too.
	writePayload := `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"MAGUS.md"}}`
	req, ok = decodeHookEnvelope(writePayload)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath, "a file_path payload must be judged as a path, not as a command")

	// Anything that is not a usable envelope is left alone for the bare-command form.
	for _, raw := range []string{
		"magus run ci | tail",           // a plain command
		`{"tool_input":{}}`,             // an envelope with nothing to judge
		`{not json`,                     // malformed
		`{"tool_input":{"command":""}}`, // explicitly empty
	} {
		_, ok := decodeHookEnvelope(raw)
		assert.False(t, ok, "must fall through to the literal form: %q", raw)
	}
}

// TestEnforceVerdictBlocksOnlyDeny pins the half that makes the guard real. Every rule was
// reachable and correct while the process exited 0, so a host read success and ran the
// command anyway: the guard looked installed and enforced nothing.
func TestEnforceVerdictBlocksOnlyDeny(t *testing.T) {
	text := OutputOptions{Format: FormatText}
	err := enforceVerdict(text, guardVerdict{Decision: "deny", Reason: "no"})
	require.Error(t, err, "a deny must exit non-zero or it blocks nothing")
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.exitCode)

	assert.NoError(t, enforceVerdict(text, guardVerdict{Decision: "advise", Context: "fyi"}),
		"advice teaches and must never block")
	assert.NoError(t, enforceVerdict(text, guardVerdict{Decision: "pass"}))

	// The exit code is the enforcement and does not depend on the rendering: a structured
	// consumer that got a zero status would be told the same lie in a different shape.
	require.Error(t, enforceVerdict(OutputOptions{Format: FormatJSON}, guardVerdict{Decision: "deny", Reason: "no"}))
}

// TestGuardDenyPrintsItsReasonOnce: text mode already renders the full reason to stdout, and
// every guard template this repo ships reads the verdict from stdout - one discards stderr
// outright - so an unconditional stderr copy reached nobody who lacked another channel and
// simply printed a kilobyte-plus reason twice to a terminal. A structured format renders no
// prose, so there stderr is the only readable channel and keeps it.
func TestGuardDenyPrintsItsReasonOnce(t *testing.T) {
	deny := guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "deny", Reason: "because"}

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
