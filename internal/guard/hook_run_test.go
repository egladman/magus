package guard

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/iotest"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/step"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// reportedExit is what errors.As finds in an exit.Reported failure.
type reportedExit interface {
	AlreadyReported() bool
	ExitCode() int
}

// testHookEnv is a HookEnv that opens no workspace: these tests judge by the built-in rules
// alone, and the cases that load a workspace's rules stand up their own checkout in cmd/magus,
// where the openers live.
func testHookEnv() HookEnv {
	self := func() (string, error) { return os.Executable() }
	writer := output.Writer{
		Codec: output.Codec{Marshal: codec.Marshal, Unmarshal: codec.Unmarshal},
		Funcs: template.FuncMap{"toJson": func(v any) string {
			b, _ := codec.Marshal(v)
			return string(b)
		}},
	}
	return HookEnv{
		Version:        "test",
		BinaryPath:     self,
		HookBinaryPath: self,
		FindRoot:       func(string) (string, error) { return "", errors.New("no workspace rules in this test") },
		Inspect: func(context.Context, string, string) (types.WorkspaceRepository, error) {
			return nil, errors.New("no workspace in this test")
		},
		CacheDir: func(string) func(string) (string, error) {
			return func(string) (string, error) { return "", errors.New("no cache dir in this test") }
		},
		LoadedCacheDir: func(string) (string, error) { return "", errors.New("no cache dir in this test") },
		Emit: func(out io.Writer, opts output.Options, decision string, verdict any) error {
			if opts.Format == output.Name {
				_, err := fmt.Fprintln(out, decision)
				return err
			}
			return writer.Write(out, opts, verdict)
		},
	}
}

// hookStdin runs the hook with its input on stdin, which is the shape a host's pre-tool-use
// hook uses. args carry the flags a wrapper passes and, last, the operands.
func hookStdin(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	return hookCommand(ctx, in, out, os.Stderr, args)
}

// hookCommand parses args the way the command line does, for the flags these tests pass:
// the shell flags, -o and -q.
func hookCommand(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) error {
	fset := flag.NewFlagSet("shell", flag.ContinueOnError)
	sf := flaggen.BindShell(fset)
	format := fset.String("o", "", "")
	quiet := fset.Bool("q", false, "")
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	opts, err := output.Parse(*format)
	if err != nil {
		return err
	}
	call := HookCall{Flags: *sf, Output: opts, Operands: fset.Args(), Argv: append([]string{"shell"}, args...), Quiet: *quiet}
	return testHookEnv().RunHook(ctx, call, in, out, errOut)
}

// reorderFlagsFirst moves the flags ahead of the operands, as the command line allows them
// anywhere. Every flag these tests pass before an operand takes a value or is a switch the
// shell command binds, so only value-taking ones need their argument kept with them.
func reorderFlagsFirst(fset *flag.FlagSet, args []string) []string {
	var flags, operands []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			operands = append(operands, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if f := fset.Lookup(name); f != nil && !strings.Contains(a, "=") && i+1 < len(args) {
			if _, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return append(flags, operands...)
}

// hookFleet stands up a workspace root and a lease ledger holding leases, and
// returns the context pinning both, the workspace root, and the resolved cache dir.
func hookFleet(t *testing.T, leases ...jobtypes.Job) (ctx context.Context, root, cacheDir string) {
	t.Helper()
	root, cacheDir = fixture.SeedLeases(t, leases...)
	return WithLocation(t.Context(), cacheDir, root, ""), root, cacheDir
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
		ctx := WithLocation(context.Background(), auditDir, "/repo/magus", "")
		// A deny now exits non-zero, which is the whole point of the guard: the
		// rendered verdict on stdout is unchanged, and the error is the blocking
		// signal a host reads. Anything OTHER than that is a real failure.
		if err := hookStdin(ctx, strings.NewReader(stdin), &out, args); err != nil {
			var silent reportedExit
			require.ErrorAs(t, err, &silent)
			require.Equal(t, guardDenyExitCode, silent.ExitCode())
		}
		return out.String()
	}

	// The label carries the rule name, which is the only route a person has from a
	// verdict to `magus describe rule <name>`.
	assert.True(t, strings.HasPrefix(run("git commit -m x"), "advise [stage-classify]: "))
	assert.True(t, strings.HasPrefix(run("git stash"), "deny [whole-tree]: "))

	// A repeat in the same session, so the structured form carries the short reason.
	got := run("git stash", "-o", "json")
	assert.Contains(t, got, `"decision": "deny"`)
	assert.Contains(t, got, `"api_version": "safetyscissors.verdict/v1alpha1"`)
	assert.Contains(t, got, `"reason": "denied again [whole-tree]: `)

	// A template renders a host dialect; defer renders empty, deny fills it.
	tpl := `template={{if eq .decision "deny"}}{"permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}{{end}}`
	assert.Contains(t, run("git stash", "-o", tpl), `"permissionDecision":"deny"`)
	assert.Empty(t, strings.TrimSpace(run("ls", "-o", tpl)))

	// -o name is the bare decision word.
	assert.Equal(t, "deny\n", run("git stash", "-o", "name"))

	// Fail open on empty stdin.
	assert.Equal(t, "defer\n", run(""))
}

// TestShellRefusesAnUnquotedCommand: several operands means the quotes were left off, and
// judging the first word alone would clear `go` and report a pass for a command nobody ran.
func TestShellRefusesAnUnquotedCommand(t *testing.T) {
	var out strings.Builder
	err := hookCommand(context.Background(), strings.NewReader(""), &out, os.Stderr,
		[]string{"git", "stash"})
	require.ErrorContains(t, err, "quote the whole command")
	assert.Contains(t, err.Error(), "git stash", "the refusal hands back what to type")
}

// TestHookCmd_UnreadableStdinFailsClosed pins the one input case that does NOT
// fail open. An empty stdin is a host that sent nothing; a stdin that ERRORS is a
// payload that was lost in flight, and answering pass there reports a command the
// guard never saw as cleared. The manpage's exit table promises the same: deny and
// unreadable input share code 2 so a host that blocks on 2 fails closed in both.
func TestHookCmd_UnreadableStdinFailsClosed(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")

	// A truncated payload, which is the shape that actually occurs: some bytes
	// arrive and the rest never does.
	truncated := func() io.Reader {
		return io.MultiReader(strings.NewReader("git sta"), iotest.ErrReader(errors.New("read |0: file already closed")))
	}

	var out bytes.Buffer
	err := hookStdin(ctx, truncated(), &out, []string{"-o", "name"})
	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.ExitCode())
	assert.Equal(t, "deny\n", out.String())

	// The reason names what happened and what to do about it, so a blocked agent is
	// not left guessing at a guard it cannot see.
	out.Reset()
	require.Error(t, hookStdin(ctx, truncated(), &out, []string{"-o", "json"}))
	assert.Contains(t, out.String(), "could not read its input from stdin")
	assert.Contains(t, out.String(), "file already closed")

	// --observe carries no verdict, so it keeps the documented "always exits 0".
	out.Reset()
	require.NoError(t, hookStdin(ctx, truncated(), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())
}

// hookCommandEvent is the trail event a shell hook is expected to append for one command or
// path: everything the hook sets, with the clock, the content-addressed blobs and the verdict's
// provenance, which other tests pin, taken from got so a new field still has to match.
func hookCommandEvent(ctx context.Context, got journal.Call, origin jobtypes.Origin, action, preview string) journal.Call {
	return journal.Call{
		Time:      got.Time,
		Kind:      journal.CallKindAgentCommand,
		Origin:    trail.StampOrigin(ctx, origin),
		Workspace: "/repo/magus",
		Name:      action,
		Outcome:   journal.OutcomeOK,
		Request:   got.Request,
		Response:  got.Response,
		Preview:   preview,
		Guard:     got.Guard,
	}
}

// loggedCalls is the newest limit calls in the log under base.
func loggedCalls(ctx context.Context, base string, limit int) ([]journal.Call, error) {
	var out []journal.Call
	for c, err := range journal.Calls(ctx, trail.CallsDir(base), limit) {
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// loggedPayload is the payload ref names in the log under base.
func loggedPayload(base, ref string) ([]byte, error) {
	return journal.Payload{Ref: ref}.Read(trail.CallsDir(base))
}

// A hook row times the rule load against the hook's deadline, which starts before it, and
// ends with the whole judgment.
func TestHookCmd_RecordsEachPhase(t *testing.T) {
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), io.Discard, []string{"-o", "name"}))

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Guard)
	phases := events[0].Guard.Phases
	require.Len(t, phases, 4)
	assert.Equal(t, "load_rules", phases[0].Name)
	assert.Positive(t, phases[0].BudgetMs)
	assert.Less(t, phases[0].BudgetMs, (10 * time.Second).Milliseconds(), "inside the host's kill line")
	assert.False(t, phases[0].Lapsed)
	assert.Equal(t, "builtin_rules", phases[1].Name)
	assert.Equal(t, "notices", phases[2].Name)
	assert.Equal(t, "judge", phases[3].Name)
}

// assertJudgedBy pins the judging magus a hook row records, the running binary and its
// version, and the phases it timed, and clears them from got so the rest of the row can be
// compared whole.
func assertJudgedBy(t *testing.T, got *journal.Call) {
	t.Helper()
	require.NotNil(t, got.Guard, "a hook row names the magus that judged it")
	g := *got.Guard
	assert.NotEmpty(t, g.Binary, "a hook row names the magus that judged it")
	assert.Equal(t, testHookEnv().Version, g.BinaryVersion)
	names := make([]string, len(g.Phases))
	for i, p := range g.Phases {
		names[i] = p.Name
	}
	// A spawn is judged apart from the shell rules.
	if got.Kind == journal.CallKindAgentSpawn {
		assert.Equal(t, []string{"load_rules", "judge"}, names)
	} else {
		assert.Equal(t, []string{"load_rules", "builtin_rules", "notices", "judge"}, names)
	}
	g.Binary, g.BinaryVersion, g.Phases, g.Duration = "", "", nil, 0
	got.Guard = orNil(g)
}

func callDecidedBy(c journal.Call) string {
	if c.Guard == nil {
		return ""
	}
	return c.Guard.DecidedBy
}

func phasesOf(c journal.Call) []journal.Phase {
	if c.Guard == nil {
		return nil
	}
	return c.Guard.Phases
}

// orNil is &g, or nil when g records nothing.
func orNil(g journal.Guard) *journal.Guard {
	if g.Duration == 0 && g.Verdict == (journal.Payload{}) && g.PolicyDigest == "" && g.DecidedBy == "" &&
		g.Binary == "" && g.BinaryVersion == "" && g.Cwd == "" && len(g.Phases) == 0 {
		return nil
	}
	return &g
}

func TestHookCmd_AppendsNormalizedActivity(t *testing.T) {
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	// git stash is denied, so hookCmd reports it by exiting non-zero while still
	// rendering the verdict in the requested format.
	err := hookStdin(ctx, strings.NewReader("git stash"), &out, []string{"-o", "name"})
	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	require.Equal(t, guardDenyExitCode, silent.ExitCode())
	assert.Equal(t, "deny\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	// A piped call is the hook's.
	assert.Equal(t, hookCommandEvent(ctx, got, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook}, "shell.command", "guard: deny"), got)

	body, err := loggedPayload(dir, got.Request.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","tool":"shell.command","command":"git stash","thread":"unknown","thread_from":"inferred"}`, string(body))
	body, err = loggedPayload(dir, got.Response.Ref)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"api_version":"magus.agent-command/v1alpha1"`)
	assert.Contains(t, string(body), `"decision":"deny"`)
	assert.Contains(t, string(body), `"reason":`)
}

func TestHookCmd_PathAndEmptyInputActivity(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--path", "-o", "name"}))
	assert.Equal(t, "advise\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, hookCommandEvent(ctx, got, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook}, "file.write", "guard: advise"), got)
	body, err := loggedPayload(dir, got.Request.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","path":"AGENTS.md","tool":"file.write","thread":"unknown","thread_from":"inferred"}`, string(body))

	emptyDir := t.TempDir()
	emptyCtx := WithLocation(context.Background(), emptyDir, "/repo/magus", "")
	out.Reset()
	require.NoError(t, hookStdin(emptyCtx, strings.NewReader(""), &out, nil))
	assert.Equal(t, "defer\n", out.String())
	events, err = loggedCalls(ctx, emptyDir, 1)
	require.NoError(t, err)
	assert.Empty(t, events, "a hook with no command/path has no observable invocation to record")
}

// TestHookCmd_RecordsHostAttribution covers the --agent-name/--session/--event flags: the wrapper is
// the only party that knows which agent host ran the hook, so what it passes must survive onto
// the event line, not only into the request blob.
func TestHookCmd_RecordsHostAttribution(t *testing.T) {
	// The whole-struct assertion below includes Lease, which the hook reads off the
	// environment, so a developer or CI job that exported one would fail this test over
	// its own baggage.
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &out,
		[]string{"--agent-name", "claude-code", "--session", "abc123", "--event", "PreToolUse", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]

	// Whole-struct assertion with the content-addressed and clock-dependent fields lifted out
	// first, so a new field on Event cannot be silently dropped by the hook producer.
	requestRef, responseRef := got.Request.Ref, got.Response.Ref
	got.Time, got.Request, got.Response = time.Time{}, journal.Payload{}, journal.Payload{}
	assertJudgedBy(t, &got)
	// A bare command names no directory, so the row records the one the judge ran in.
	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, journal.Call{
		Kind:      journal.CallKindAgentCommand,
		Origin:    trail.StampOrigin(ctx, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook, Host: "claude-code", Session: "abc123"}),
		Workspace: "/repo/magus",
		Name:      "shell.command",
		Outcome:   journal.OutcomeOK,
		Preview:   "guard: defer",
		Guard:     &journal.Guard{Cwd: wd},
	}, got)

	body, err := loggedPayload(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","host":"claude-code","session":"abc123","event":"PreToolUse","tool":"shell.command","command":"ls",`+
		`"thread":"main","thread_from":"inferred","shadow":{"off":"defer","light":"defer","standard":"defer","strict":"defer"}}`, string(body))
	body, err = loggedPayload(dir, responseRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","decision":"defer"}`, string(body))
}

// TestHookCmd_AttributionIsOptional holds the fail-open contract: attribution is best-effort
// metadata, so a wrapper that supplies none still gets a verdict and still records an event.
func TestHookCmd_AttributionIsOptional(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &out, []string{"-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Empty(t, got.Host)
	assert.Empty(t, got.Session)

	// Omitted rather than recorded empty: the blob says nothing was known, not that the host is "".
	body, err := loggedPayload(dir, got.Request.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","tool":"shell.command","command":"ls","thread":"unknown","thread_from":"inferred"}`, string(body))
}

// TestHookCmd_ObserveRecordsWithoutJudging pins what --observe is for. AGENTS.md is the exact
// path TestHookCmd_PathAndEmptyInputActivity gets an ADVISE for as a write, so a pass here is
// specifically the observation being exempted from the write rules rather than the rule
// failing to fire. Without it, a hook wired to a host's read tool would advise "you are
// editing a declared output" at a file the agent only opened.
func TestHookCmd_ObserveRecordsWithoutJudging(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	// A reach is recorded under its own label, not as a write. Its preview is "observed", not
	// "guard: defer". The trail already distinguishes the two, and recording a verdict here
	// would have every read claim the guard ran and cleared it: the exact conflation --observe
	// exists to remove. The wire verdict the host reads is still defer.
	assert.Equal(t, hookCommandEvent(ctx, got, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook}, "file.read", "observed"), got)

	body, err := loggedPayload(dir, got.Request.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","tool":"file.read","path":"AGENTS.md","thread":"unknown","thread_from":"inferred"}`, string(body))
	body, err = loggedPayload(dir, got.Response.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","decision":""}`, string(body))
}

// TestHookCmd_ObserveOutranksPath holds the precedence the wrapper depends on: a host whose
// read event carries a file_path will send --observe alongside the envelope that sets --path,
// and the observation must win. The reverse would silently restore the false advisory.
func TestHookCmd_ObserveOutranksPath(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--path", "--observe", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "file.read", events[0].Name)
}

// TestHookCmd_RecordsTranscriptPath covers the session-to-transcript link. The id groups a
// session's events; the path is what a reader follows to see the rest. magus records the
// POINTER and never opens the file, which is what keeps the trail paths-and-timings.
func TestHookCmd_RecordsTranscriptPath(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	envelope := `{"hook_event_name":"PreToolUse","session_id":"s1","transcript_path":"/tmp/t.jsonl","tool_input":{"command":"ls"}}`
	require.NoError(t, hookStdin(ctx, strings.NewReader(envelope), &out, []string{"-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "s1", got.Session)

	body, err := loggedPayload(dir, got.Request.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","session":"s1","transcript":"/tmp/t.jsonl","event":"PreToolUse","tool":"shell.command","command":"ls","thread":"unknown","thread_from":"inferred"}`, string(body))
}

// TestHookCmd_TranscriptFlagRecordsThePointer covers the FLAG path, which is the one the
// shipped observe template actually uses. That template extracts the path with jq and pipes
// plain text rather than the whole event, so nothing about the envelope is available to it;
// without the flag the transcript link exists only for hosts that pipe raw JSON, which is
// none of the ones magus ships a template for.
func TestHookCmd_TranscriptFlagRecordsThePointer(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("internal/trail/trail.go"), &out,
		[]string{"--observe", "--agent-name", "claude-code", "--session", "s9", "--transcript", "/tmp/t.jsonl", "--event", "PreToolUse", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t,
		hookCommandEvent(ctx, got, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook, Host: "claude-code", Session: "s9"}, "file.read", "observed"),
		got)

	body, err := loggedPayload(dir, got.Request.Ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-command/v1alpha1","host":"claude-code","session":"s9","transcript":"/tmp/t.jsonl","event":"PreToolUse","tool":"file.read","path":"internal/trail/trail.go","thread":"main","thread_from":"inferred"}`, string(body))
}

// TestHookCmd_ObserveWithNoInputRecordsNothing: a wrapper whose host event carried no path
// has nothing to report, and an observation with no subject is dropped like any other empty
// one rather than being invented as ".", which would claim a reach the host never described.
func TestHookCmd_ObserveWithNoInputRecordsNothing(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(""), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	assert.Empty(t, events)
}

// TestHookCmd_RecordsSpawnFromEnvelope covers the spawn hook end to end: a host payload
// carrying a prompt rather than a command is recorded as a spawn, with the handed context in the
// blob and the cooperative lease marker stamped onto the event.
//
// It also pins two things that must NOT happen. The prompt below quotes `git stash`, which the
// command guard denies. A spawn is not a guard input, so the verdict is a pass and the
// spawn is recorded rather than blocked for describing a denied command; and that stays true
// whether or not the caller named a model, which is a claim this guard grades nothing on.
//
// The session loads the multi-agent skill first: spawn-before-skill
// grades that. Without it the pass below would be a deny for an unrelated
// reason; TestHookCmd_DeniesSpawnBeforeTheBrief carries that rule.
func TestHookCmd_RecordsSpawnFromEnvelope(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	loadSkillForTest(t, ctx, dir, "abc123")
	envelope := `{"hook_event_name":"PreToolUse","session_id":"abc123","tool_name":"Task",` +
		`"tool_input":{"description":"audit the store","subagent_type":"Explore","model":"opus",` +
		`"prompt":"lease: notes-store-6b\nDo not run git stash anywhere."}}`

	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(envelope), &out,
		[]string{"--agent-name", "claude-code", "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]

	requestRef := got.Request.Ref
	got.Time, got.Request = time.Time{}, journal.Payload{}
	assertJudgedBy(t, &got)
	assert.Equal(t, journal.Call{
		Kind:      journal.CallKindAgentSpawn,
		Origin:    trail.StampOrigin(ctx, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook, Host: "claude-code", Session: "abc123"}),
		Workspace: "/repo/magus",
		Name:      "Explore",
		Lease:     "notes-store-6b",
		Outcome:   journal.OutcomeOK,
	}, got)

	body, err := loggedPayload(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_version":"magus.agent-spawn/v1alpha1","host":"claude-code","session":"abc123","event":"PreToolUse",`+
		`"tool":"Task","child":"Explore","lease":"notes-store-6b","declared_model":"opus",`+
		`"context":"lease: notes-store-6b\nDo not run git stash anywhere.","thread":"main","thread_from":"inferred"}`, string(body))
}

// TestHookCmd_SpawnWithoutMarkerOrLabel holds three parts of the cooperative contract: an
// orchestrator that writes no marker still gets an audited spawn record, just an uncorrelated
// one; a host whose payload names no callee still records a spawn; and a spawn that names no
// model records the absence distinguishably from one that never named the field at all; both
// read back as "" here, and it is [renderSessionShow]'s job to word that as "none declared"
// rather than let it read as blank-looks-fine.
func TestHookCmd_SpawnWithoutMarkerOrLabel(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")

	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(`{"tool_input":{"prompt":"go and audit the store"}}`),
		&out, []string{"-o", "name"}))
	assert.Equal(t, "defer\n", out.String())

	events, err := loggedCalls(ctx, dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	// No marker, so the spawn is uncorrelated: no lease.
	uncorrelated := events[0]
	assertJudgedBy(t, &uncorrelated)
	assert.Equal(t, journal.Call{
		Time:      events[0].Time,
		Kind:      journal.CallKindAgentSpawn,
		Origin:    trail.StampOrigin(ctx, jobtypes.Origin{EntryPoint: jobtypes.EntryPointHook}),
		Workspace: "/repo/magus",
		Name:      "agent.spawn",
		Outcome:   journal.OutcomeOK,
		Request:   events[0].Request,
	}, uncorrelated)

	body, err := loggedPayload(dir, events[0].Request.Ref)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "declared_model")
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
			ctx := WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")
			input := ""
			if tt.name == "unclassifiable path" {
				input = "/nonexistent/elsewhere.txt"
			}
			require.NoError(t, hookStdin(ctx, strings.NewReader(input), &out, tt.args))
			assert.Equal(t, "defer\n", out.String(),
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

	root := t.TempDir()
	t.Chdir(root)
	installHostRows(t, "claude-code", types.Limits{}, types.HarnessTool{Name: "TodoWrite", Capability: types.ToolInert})
	var out bytes.Buffer
	ctx := WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")
	require.NoError(t, hookStdin(ctx, strings.NewReader(payload), &out, []string{"-o", "name", "--agent-name", "claude-code"}))
	assert.Equal(t, "defer\n", out.String())
}

// shellStep runs `magus shell` over step's record from a host whose rows
// call TodoWrite inert. It returns the verdict's name and the exit error.
func shellStep(t *testing.T, step step.Step) (string, error) {
	t.Helper()
	t.Setenv(trail.EnvBaggage, "")
	t.Chdir(t.TempDir())
	installHostRows(t, "claude-code", types.Limits{}, types.HarnessTool{Name: "TodoWrite", Capability: types.ToolInert})
	raw, err := codec.Marshal(step)
	require.NoError(t, err)
	var out bytes.Buffer
	ctx := WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")
	err = hookStdin(ctx, bytes.NewReader(raw), &out, []string{"-o", "name", "--agent-name", "claude-code"})
	return out.String(), err
}

func TestShellJudgesAToolStepAsTheEnvelopeItStandsFor(t *testing.T) {
	step := step.Step{APIVersion: step.APIVersion, Kind: "tool", Name: "TodoWrite", Session: "s1",
		Input: map[string]any{"todos": []any{map[string]any{"content": "never use sed -i here"}}}}

	verdict, err := shellStep(t, step)

	require.NoError(t, err)
	assert.Equal(t, "defer\n", verdict, "an inert tool's step passes as its envelope does")
}

func TestShellDeniesAStepOfAKindNoRowDeclares(t *testing.T) {
	step := step.Step{APIVersion: step.APIVersion, Kind: "refund", Name: "card", Session: "s1"}

	verdict, err := shellStep(t, step)

	require.Error(t, err, "a deny exits 2")
	assert.Equal(t, "deny\n", verdict)
}

// TestHookCmdAdvisesOncePerSession is the same rule through the command the host actually
// runs: a held advisory prints its verdict and the ref to its full text once per session,
// and its brief after.
func TestHookCmdAdvisesOncePerSession(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	base, root := t.TempDir(), t.TempDir()
	ctx := WithLocation(t.Context(), base, root, "")
	run := func(session string) string {
		var out strings.Builder
		// The display flags live on a package global, so one case must not leak into the
		// next; TestHookCmd resets the same way and for the same reason.
		require.NoError(t, hookStdin(ctx, strings.NewReader("cat handler.go"), &out, []string{"--session", session}))
		return out.String()
	}

	first := run("session-1")
	require.True(t, strings.HasPrefix(first, "advise [source-read]: "))
	lines := strings.Split(strings.TrimSpace(first), "\n")
	require.Len(t, lines, 2, "the advice and the ref: %q", first)
	assert.Contains(t, lines[0], "magus refs", "the advice names the command to run")
	assert.NotContains(t, first, "SCIP symbol indexes", "the rationale is behind the ref")
	ref := regexp.MustCompile(`^details: \S*magus query output (grd[0-9a-f]{16})$`).FindStringSubmatch(lines[1])
	require.Len(t, ref, 2, "line %q must cite a grd ref", lines[1])
	stored, err := loggedPayload(base, ref[1])
	require.NoError(t, err)
	assert.Contains(t, string(stored), "SCIP symbol indexes", "the ref holds the rationale")
	assert.True(t, strings.HasSuffix(string(stored), "\nsee: https://eli.gladman.cc/magus/reference/rules/source-read/"), "with the rule's page, got %q", stored)
	served := hint.ReadServedNext(base)
	require.NotEmpty(t, served)
	assert.Equal(t, "advice-verdict", served[len(served)-1].ID, "the ref line is counted as a hint")

	repeat := run("session-1")
	assert.NotContains(t, repeat, "details:", "the brief has no rationale to store")
	assert.Contains(t, repeat, "magus refs", "the repeat still names the command, which is what converts")
	assert.Less(t, len(repeat), len(first), "a repeat nobody has to read around")

	assert.Contains(t, run("session-2"), "details:", "a fresh session is owed the fact once")
}

// TestHookCmdShortensARepeatedDenial pins both forms of a deny. A refusal names its problem
// every time, since a second `git stash` blocked with no reason is a dead end, but only the
// first firing in a session spends the verdict sentence: the repeat is one line naming the
// rule and the ref that holds the full verdict. The rationale and the rule's page live only
// behind that ref, and the ref must resolve to that verdict.
func TestHookCmdShortensARepeatedDenial(t *testing.T) {
	testkit.Isolate(t)
	base, root := t.TempDir(), t.TempDir()
	ctx := WithLocation(t.Context(), base, root, "")
	run := func(command, session string) string {
		var out strings.Builder
		err := hookStdin(ctx, strings.NewReader(command), &out, []string{"--session", session})
		var silent reportedExit
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.ExitCode())
		return out.String()
	}
	const see = "\nsee: https://eli.gladman.cc/magus/reference/rules/whole-tree/"

	first := run("git stash", "session-1")
	assert.Len(t, strings.Split(strings.TrimSpace(first), "\n"), 2, "the verdict and the ref: %q", first)
	assert.NotContains(t, first, see, "the rule's page is in the stored verdict")
	assert.NotContains(t, first, "denied again")

	repeat := run("echo hi && git stash", "session-1")
	lines := strings.Split(strings.TrimSpace(repeat), "\n")
	require.Len(t, lines, 3, "repeat: %q", repeat)
	doc, ok := Rule("whole-tree")
	require.True(t, ok)
	assert.Equal(t, "deny [whole-tree]: denied again [whole-tree]: "+doc.Catches, lines[0])
	assert.Equal(t, "nothing ran (2 commands)", lines[1], "the repeat still says how much of the line was refused")
	ref := regexp.MustCompile(`^details: \S*magus query output (grd[0-9a-f]{16})$`).FindStringSubmatch(lines[2])
	require.Len(t, ref, 2, "line %q must cite a grd ref", lines[2])

	stored, err := loggedPayload(base, ref[1])
	require.NoError(t, err)
	assert.Contains(t, string(stored), "\nnothing ran (2 commands)\n", "the stored verdict is the full form of THIS command, got %q", stored)
	assert.True(t, strings.HasSuffix(string(stored), see), "with the rule's page, got %q", stored)
	assert.NotContains(t, string(stored), "denied again")

	served := hint.ReadServedNext(base)
	require.NotEmpty(t, served)
	assert.Equal(t, "deny-verdict", served[len(served)-1].ID, "the ref line is counted as a hint")

	assert.NotContains(t, run("git stash", "session-2"), "denied again", "a fresh session hears the rule in full")
}

// TestHookCmdRoutesAnAgentSourceWrite pins the WIRING, not the rule: a rule that is
// correct and never called is the failure mode this repository has shipped before. It also
// pins the dedupe on the path input, where a suppressed advisory must leave silence
// rather than let the next rung speak into it.
func TestHookCmdRoutesAnAgentSourceWrite(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	w := testkit.NewWorkspace(t)
	root := w.Root()
	t.Chdir(root)
	w.Magusfile("")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "magus"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755))

	ctx := WithLocation(t.Context(), t.TempDir(), root, "")
	run := func() string {
		var out strings.Builder
		require.NoError(t, hookStdin(ctx, strings.NewReader("internal/agent/skills/magus-run/SKILL.md"),
			&out, []string{"--path", "--session", "session-1"}))
		return out.String()
	}

	assert.Contains(t, run(), "magus-skill-authoring")
	assert.Equal(t, "defer\n", run(),
		"the repeat is silence, not the new-directory advisory stepping into the gap this rule left")
}

// TestHookCmdJudgesTheCacheDirOnBothInputs is the hook's decision table for this rule.
// It is here rather than in TestEvaluateBashGuard because the rule is not pure: it reads
// the resolved cache location, so the table it belongs in is the one that runs hookCmd.
//
// The rows run UNBOUND, which is the contract: this is the guard's own evidence rather
// than a boundary, so an orchestrator and a person in their own checkout are refused too.
func TestHookCmdJudgesTheCacheDirOnBothInputs(t *testing.T) {
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
		"a source file":           {input: "guard_cache.go", path: true, want: "defer\n"},
		"a sibling directory":     {input: ".magus-notes/a.md", path: true, want: "defer\n"},
		"appending to a journal":  {input: "echo x >> .magus/advisories/anon.served-next", want: "deny\n"},
		"removing the whole dir":  {input: "rm -rf .magus", want: "deny\n"},
		"an in-place marker edit": {input: "sed -i 's/a/b/' .magus/lease", want: "deny\n"},
		"copying over the marker": {input: "cp foo .magus/lease", want: "deny\n"},
		"reading a log":           {input: "cat .magus/logs/x.log", want: "defer\n"},
		"reading a captured run":  {input: "./magus query output outabc", want: "defer\n"},
		"binding through magus":   {input: "./magus session lease adj/x", want: "defer\n"},
		"a sibling name":          {input: "echo x >> .magus-notes/a.md", want: "defer\n"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, root, _ := hookFleet(t)
			// The fixture's cache dir is already elsewhere, so these rows exercise the
			// literal spelling against a RELOCATED dir, which is the harder half.
			require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))

			args := []string{"-o", "name"}
			if tc.path {
				args = append(args, "--path")
			}
			var out bytes.Buffer
			err := hookStdin(ctx, strings.NewReader(tc.input), &out, args)
			if tc.want == "deny\n" {
				require.Error(t, err, "a deny that exits 0 blocks nothing")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, out.String())
		})
	}
}

// TestHookCmdDeniesTheCacheDirAheadOfTheBoundaryItSitsIn is the rank the rule was written for:
// a worker handed write paths that cover the dir must read what the dir IS, not a verdict
// about whose boundary it is.
func TestHookCmdDeniesTheCacheDirAheadOfTheBoundaryItSitsIn(t *testing.T) {
	lease := narrowLease()
	lease.Paths.Write = []string{"**"}
	// Unregistered, so the write-path rule has a denial of its own to be outranked BY. A
	// boundary that covers the path and says nothing leaves the rank unobserved.
	lease.Registered = 0
	ctx, _, _ := hookFleet(t, lease)

	var out bytes.Buffer
	err := hookStdin(ctx, strings.NewReader(".magus/lease"), &out,
		[]string{"--path", "--lease", lease.ID, "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "magus cache dir")
	assert.Contains(t, out.String(), "which only magus writes")
	assert.NotContains(t, out.String(), "registered the base it landed on",
		"the write-path rules must not answer for this path")
}

// TestHookCmdDeniesTheGateUnderANarrowLease proves the WIRING. The rule itself is covered
// above; what this pins is that hookCmd reaches it, because a rule nothing calls never
// fires however well it is tested.
func TestHookCmdDeniesTheGateUnderANarrowLease(t *testing.T) {
	// The hook falls back to the environment for the lease, so a developer or CI job that
	// exported one would decide the control case below.
	ctx, _, _ := hookFleet(t, narrowLease())
	command := "./magus affected ci --no-default-charms"

	var denied bytes.Buffer
	err := hookStdin(ctx, strings.NewReader(command), &denied,
		[]string{"--lease", "harness/lease-scoped-deny", "-o", "name"})
	require.Error(t, err, "a deny that exits 0 blocks nothing: the host runs the command anyway")
	assert.Equal(t, "deny\n", denied.String())

	var unleased bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(command), &unleased, []string{"-o", "name"}))
	assert.Equal(t, "defer\n", unleased.String(), "a caller naming no lease is scoped by nobody's row")
}

// TestHookCmdJudgesTheMCPLedgerInput is the decision table for the transport the CLI
// rules would otherwise miss: the same envelope a host forwards for an MCP tool call.
func TestHookCmdJudgesTheMCPLedgerInput(t *testing.T) {
	wide := narrowLease()
	wide.Paths.Write = []string{"cmd/magus/**", "internal/hint/**"}
	ctx, _, _ := hookFleet(t, wide)

	client := func(script string) string {
		return fmt.Sprintf(`{"script":%q}`, `import "magus"; `+script)
	}
	own := fmt.Sprintf("\"%s\"", wide.ID)
	for name, tc := range map[string]struct {
		toolInput string
		want      string
	}{
		"fork on another row":   {client(`magus\workspace().putJob("harness/other", opts: {"paths": {"write": ["**"]}});`), "deny\n"},
		"fork widening its own": {client(fmt.Sprintf("magus\\workspace().putJob(%s, opts: {\"paths\": {\"write\": [\"**\"]}});", own)), "deny\n"},
		"clearing the board":    {client(`magus\workspace().clearJobs();`), "deny\n"},
		"exec elsewhere":        {client(`magus\job("harness/other").register();`), "deny\n"},
		"exec its own":          {client(fmt.Sprintf("magus\\job(%s).register();", own)), "defer\n"},
		"listing the plan":      {client(`magus\workspace().jobs();`), "defer\n"},
		"giving paths back":     {client(fmt.Sprintf("magus\\workspace().putJob(%s, opts: {\"paths\": {\"write\": [\"cmd/magus/**\"]}});", own)), "defer\n"},
		// A shrink beside a forged checkpoint or rewritten criteria is not a plain shrink.
		"forging its own base":   {client(fmt.Sprintf("magus\\workspace().putJob(%s, opts: {\"paths\": {\"write\": [\"cmd/magus/**\"]}, \"checkpoint\": \"deadbeef\"});", own)), "deny\n"},
		"rewriting its criteria": {client(fmt.Sprintf("magus\\workspace().putJob(%s, opts: {\"paths\": {\"write\": [\"cmd/magus/**\"]}, \"criteria\": \"something else\"});", own)), "deny\n"},
	} {
		envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"session_id\":\"mcp-%s\",\"tool_name\":\"mcp__magus__client\",\"tool_input\":%s}", name, tc.toolInput)
		var out bytes.Buffer
		err := hookStdin(ctx, strings.NewReader(envelope), &out, []string{"--lease", wide.ID, "-o", "name"})
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
	ctx, _, _ := hookFleet(t, narrowLease())

	var out bytes.Buffer
	err := hookStdin(ctx, strings.NewReader("magus run test ."), &out, []string{"--lease", "harness/typo", "-o", "json"})
	var silent reportedExit
	require.ErrorAs(t, err, &silent, "an assertion that does not resolve must not exit 0")
	assert.Equal(t, guardDenyExitCode, silent.ExitCode())
	assert.Contains(t, out.String(), `"decision": "deny"`)
	assert.Contains(t, out.String(), "is not declared")
	assert.Contains(t, out.String(), `"lease": "harness/typo"`, "the verdict names the row it graded against")
}

// The other half of C3, and the lockout it caused: the rule refused the line it printed, so
// a checkout whose row had moved could not read the plan, print a schema, or bind again.
func TestHookCmdLetsAnUndeclaredLeaseRepairItself(t *testing.T) {
	ctx, _, _ := hookFleet(t, narrowLease())

	for _, command := range []string{
		"magus ls jobs",
		"magus job wait --schema",
		"magus job exec harness/real",
		"git status --short",
		"ls internal/job",
	} {
		var out bytes.Buffer
		err := hookStdin(ctx, strings.NewReader(command), &out, []string{"--lease", "harness/typo", "-o", "json"})
		require.NoError(t, err, command)
		assert.NotContains(t, out.String(), "is not declared", command)
	}

	// One reader in front of the work does not launder it.
	var out bytes.Buffer
	err := hookStdin(ctx, strings.NewReader("ls && magus run test ."), &out, []string{"--lease", "harness/typo", "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "is not declared")
}

// TestHookCmdNoticesATerminalLease covers the other half: a row that has finished still
// names a session, and every rule keyed on it has quietly stopped applying.
func TestHookCmdNoticesATerminalLease(t *testing.T) {
	done := narrowLease()
	done.State = jobtypes.StatePass
	ctx, _, _ := hookFleet(t, done)

	var first bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &first,
		[]string{"--lease", done.ID, "--session", "session-terminal"}))
	assert.Contains(t, first.String(), "is in state pass")
	assert.Contains(t, first.String(), "its rules are inert")

	var second bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &second,
		[]string{"--lease", done.ID, "--session", "session-terminal"}))
	assert.Equal(t, "defer\n", second.String(), "a standing fact is said once per session")
}

// TestHookVerdictCarriesTheActingLease pins the field on the wire, which is what lets a
// person see WHICH row decided a verdict.
func TestHookVerdictCarriesTheActingLease(t *testing.T) {
	ctx, _, _ := hookFleet(t, narrowLease())

	var leased bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &leased,
		[]string{"--lease", narrowLease().ID, "-o", "json"}))
	assert.Contains(t, leased.String(), `"lease": "harness/lease-scoped-deny"`)
	assert.Contains(t, leased.String(), `"lease_from": "flag"`)

	var unbound bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &unbound, []string{"-o", "json"}))
	assert.NotContains(t, unbound.String(), `"lease`, "a session nobody leased names no row and no source")

	// --lease takes no default from the environment, so a BAGGAGE claim reaches the verdict
	// as the claim it is rather than as a flag that would outrank every record.
	t.Setenv(trail.EnvBaggage, fmt.Sprintf("%s=%s", trail.BaggageLease, narrowLease().ID))
	var claimed bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("ls"), &claimed, []string{"-o", "json"}))
	assert.Contains(t, claimed.String(), `"lease_from": "env"`)
}

// TestHookCmdRefusesAnInvalidLeaseOnEveryInput: the check lived inside gradeLeasedWrite,
// which runs on the path input only, so a COMMAND under a typo'd id ran fully un-enrolled
// with nothing said about it.
func TestHookCmdRefusesAnInvalidLeaseOnEveryInput(t *testing.T) {
	for name, args := range map[string][]string{
		"the shell-command input": {"--lease", "has spaces", "--session", "invalid-command", "-o", "json"},
		"the file-write input":    {"--path", "--lease", "has spaces", "--session", "invalid-path", "-o", "json"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, _, _ := hookFleet(t, narrowLease())

			var out bytes.Buffer
			var silent reportedExit
			require.ErrorAs(t, hookStdin(ctx, strings.NewReader("README.md"), &out, args), &silent)
			assert.Equal(t, guardDenyExitCode, silent.ExitCode())
			assert.Contains(t, out.String(), "is not a valid lease id")
		})
	}
}

// TestHookCmdRanksTheCacheDirAboveTheUndeclaredLease: the undeclared refusal used to return
// from hookCmd before anything else ran, so it outranked the cache-dir rule against both
// files' stated order.
func TestHookCmdRanksTheCacheDirAboveTheUndeclaredLease(t *testing.T) {
	ctx, _, _ := hookFleet(t, narrowLease())

	var out bytes.Buffer
	err := hookStdin(ctx, strings.NewReader(".magus/lease"), &out,
		[]string{"--path", "--lease", "harness/nobody-declared-this", "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "magus cache dir")
	assert.NotContains(t, out.String(), "is not declared",
		"the cache dir is what the write is about; whose boundary it is comes second")
}

// TestHookCmdDeniesTheGateThroughTheMCPDoor is the hole the tool-name decode closes: the
// same work the lease-scoped gate rule refuses for shell commands, asked for through
// the tool that does it. It passed unjudged while the coverage line said deny=model.
func TestHookCmdDeniesTheGateThroughTheMCPDoor(t *testing.T) {
	lease := narrowLease()
	ctx, _, _ := hookFleet(t, lease)

	for name, toolCall := range map[string]string{
		"the affected gate": `"tool_name":"mcp__magus__client","tool_input":{"script":"import \"magus\"; fun main(args: [str]) > any !> str { return magus\\cmd(\"affected\", [\"ci\"]); }"}`,
	} {
		envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"session_id\":\"mcp-gate\",\"%s}", toolCall[1:])
		var out bytes.Buffer
		err := hookStdin(ctx, strings.NewReader(envelope), &out, []string{"--lease", lease.ID, "-o", "name"})
		require.Error(t, err, name)
		assert.Equal(t, "deny\n", out.String(), name)
	}

	// The report about the gate is not a run of it, so it still passes.
	envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-plan",` +
		`"tool_name":"mcp__magus__client","tool_input":{"script":"import \"magus\"; fun main(args: [str]) > any !> str { return magus\\cmd(\"affected\", [\"ci\", \"--plan\"]); }"}}`
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(envelope), &out, []string{"--lease", lease.ID, "-o", "name"}))
	assert.Equal(t, "defer\n", out.String())
}

// TestHookCmdStandsDownOnAServedNext is the rule in place: the same command is denied by a
// role-scoped rule and cleared once magus is the one that suggested it.
func TestHookCmdStandsDownOnAServedNext(t *testing.T) {
	ctx, _, cacheDir := hookFleet(t, narrowLease())
	gate := hint.NewGate(cacheDir, "session-preauth")
	command := "./magus affected ci --no-default-charms"

	var denied bytes.Buffer
	err := hookStdin(ctx, strings.NewReader(command), &denied,
		[]string{"--lease", narrowLease().ID, "--session", "session-preauth", "-o", "name"})
	require.Error(t, err, "the gate rule refuses a lease that was handed a narrower check")
	assert.Equal(t, "deny\n", denied.String())

	serveNext(t, gate, "gate-run", "magus", "affected", "ci", "--no-default-charms")

	var cleared bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(command), &cleared,
		[]string{"--lease", narrowLease().ID, "--session", "session-preauth", "-o", "name"}))
	assert.Equal(t, "defer\n", cleared.String(),
		"magus refusing the command magus served is the tool disagreeing with itself")
}

// TestHookCmdNeverPreauthorizesAWorkspaceWideDeny is the carve-out. These refuse work that
// cannot be undone, and they protect everyone, so a journal entry naming one buys nothing.
func TestHookCmdNeverPreauthorizesAWorkspaceWideDeny(t *testing.T) {
	ctx, _, cacheDir := hookFleet(t)
	gate := hint.NewGate(cacheDir, "session-carveout")

	for _, command := range []string{"git stash", "git reset --hard", "git clean -fdx"} {
		serveNext(t, gate, "fabricated", strings.Fields(command)...)
		var out bytes.Buffer
		err := hookStdin(ctx, strings.NewReader(command), &out,
			[]string{"--session", "session-carveout", "-o", "name"})
		require.Error(t, err, "%q", command)
		assert.Equal(t, "deny\n", out.String(), "%q", command)
	}
}

// TestHookCmdDeniesAWiringWriteUnderALease proves the WIRING, the way the gate rule's own
// test does: a rule nothing calls never fires however well it is tested. It also pins the
// rank, since the lease ledger speaks first on file writes and a boundary that happens to
// contain the file must not clear it.
func TestHookCmdDeniesAWiringWriteUnderALease(t *testing.T) {
	lease := narrowLease()
	lease.Paths.Write = []string{".claude/**", "cmd/magus/**"}
	ctx, _, _ := hookFleet(t, lease)

	var denied bytes.Buffer
	err := hookStdin(ctx, strings.NewReader(".claude/settings.json"), &denied,
		[]string{"--path", "--lease", lease.ID, "-o", "name"})
	require.Error(t, err, "a deny that exits 0 blocks nothing")
	assert.Equal(t, "deny\n", denied.String())

	var unbound bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(".claude/settings.json"), &unbound,
		[]string{"--path", "-o", "name"}))
	assert.Equal(t, "advise\n", unbound.String(), "an unbound session rewires its own hosts")
}

// TestHookCmdGradesAgainstTheLedger drives the wire contract rather than the grader: the
// flag reaches the rule, a denial exits with the blocking status, and the flag outranks
// the environment.
func TestHookCmdGradesAgainstTheLedger(t *testing.T) {
	ctx, root, cacheDir := hookFleet(t, fixture.FleetLeases()...)
	run := func(stdin string, args ...string) (string, error) {
		var out strings.Builder
		err := hookStdin(ctx, strings.NewReader(stdin), &out, args)
		return out.String(), err
	}
	owned := filepath.Join(root, "internal/ledger/store.go")

	t.Run("--lease denies a write into another lease's paths", func(t *testing.T) {
		got, err := run(owned, "--path", "--lease", "lease-b", "-o", "name")
		var silent reportedExit
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.ExitCode())
		assert.Equal(t, "deny\n", got)
	})

	t.Run("the baggage claim answers when no record does", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "userId=alice,"+trail.BaggageLease+"=lease-b")
		_, err := run(owned, "--path", "-o", "name")
		var silent reportedExit
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.ExitCode())
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

	// The claim ranks below every record. Before, --lease defaulted to BAGGAGE, so the
	// hook's inherited lease-b was passed in as a flag and outranked both of these.
	t.Run("the session's binding outranks the baggage claim", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "")
		require.NoError(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).
			Bind(job.Caller{Session: "bound-session"}, "lease-a"))
		t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-b")
		got, err := run(owned, "--path", "--session", "bound-session", "-o", "json")
		require.NoError(t, err, "graded under the binding's lease-a, whose ground this is")
		assert.Contains(t, got, `"lease": "lease-a"`)
		assert.Contains(t, got, `"lease_from": "contested"`)
	})

	t.Run("a subagent's spawn record outranks the baggage claim", func(t *testing.T) {
		t.Setenv(trail.EnvBaggage, "")
		spawned := `{"session_id":"spawn-session","hook_event_name":"PostToolUse","tool_name":"Agent",` +
			`"tool_input":{"description":"orchestrator/integrator lease-a","prompt":"Carry the job."},` +
			`"tool_response":{"status":"async_launched","agentId":"a1b2c3"}}`
		_, err := run(spawned, "--agent-name", "claude-code", "-o", "name")
		require.NoError(t, err)
		t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-b")
		got, err := run(owned, "--path", "--agent-name", "claude-code", "--session", "spawn-session", "--agent", "a1b2c3", "-o", "json")
		require.NoError(t, err, "graded under the spawn's lease-a, whose ground this is")
		assert.Contains(t, got, `"lease": "lease-a"`)
		assert.Contains(t, got, `"lease_from": "contested"`, "the claim named another lease")
	})
}

// TestHookCmdRefusesAHarnessRewireUnderEveryLeaseSource pins that the guard refuses
// `magus agent harness install` under a lease from any source. The CLI refuses it too, but
// it cannot see the subagent or the host session, so a worker attributed by either would
// otherwise rewrite the skills that steer it.
func TestHookCmdRefusesAHarnessRewireUnderEveryLeaseSource(t *testing.T) {
	const rewire = "magus agent harness install"
	spawned := `{"session_id":"spawn-session","hook_event_name":"PostToolUse","tool_name":"Agent",` +
		`"tool_input":{"description":"orchestrator/integrator lease-a","prompt":"Carry the job."},` +
		`"tool_response":{"status":"async_launched","agentId":"a1b2c3"}}`
	for name, tc := range map[string]struct {
		setup func(t *testing.T, ctx context.Context, cacheDir, root string)
		args  []string
		want  string
	}{
		"flag": {args: []string{"--lease", "lease-a"}, want: `"lease_from": "flag"`},
		"env": {
			setup: func(t *testing.T, _ context.Context, _, _ string) {
				t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=lease-a")
			},
			want: `"lease_from": "env"`,
		},
		"marker": {
			setup: func(t *testing.T, _ context.Context, cacheDir, _ string) {
				bindCheckout(t, cacheDir, "lease-a")
			},
			want: `"lease_from": "marker"`,
		},
		"session": {
			setup: func(t *testing.T, _ context.Context, cacheDir, root string) {
				require.NoError(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).
					Bind(job.Caller{Session: "bound-session"}, "lease-a"))
			},
			args: []string{"--session", "bound-session"},
			want: `"lease_from": "agent"`,
		},
		"agent": {
			setup: func(t *testing.T, ctx context.Context, _, _ string) {
				require.NoError(t, hookStdin(ctx, strings.NewReader(spawned), io.Discard, []string{"--agent-name", "claude-code", "-o", "name"}))
			},
			args: []string{"--agent-name", "claude-code", "--session", "spawn-session", "--agent", "a1b2c3"},
			want: `"lease_from": "agent"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, root, cacheDir := hookFleet(t, fixture.FleetLeases()...)
			if tc.setup != nil {
				tc.setup(t, ctx, cacheDir, root)
			}
			var out bytes.Buffer
			err := hookStdin(ctx, strings.NewReader(rewire), &out, append(tc.args, "-o", "json"))
			require.Error(t, err, "a bound worker is refused the harness")
			assert.Contains(t, out.String(), "would rewrite the skills that steer lease lease-a's calls")
			assert.Contains(t, out.String(), tc.want)
		})
	}

	t.Run("an unbound caller rewires its own hosts", func(t *testing.T) {
		ctx, _, _ := hookFleet(t, fixture.FleetLeases()...)
		var out bytes.Buffer
		require.NoError(t, hookStdin(ctx, strings.NewReader(rewire), &out, []string{"-o", "json"}))
		assert.NotContains(t, out.String(), "leave the host harness alone")
	})

	t.Run("verify is a read", func(t *testing.T) {
		ctx, _, _ := hookFleet(t, fixture.FleetLeases()...)
		var out bytes.Buffer
		_ = hookStdin(ctx, strings.NewReader("magus agent harness verify"), &out, []string{"--lease", "lease-a", "-o", "json"})
		assert.NotContains(t, out.String(), "leave the host harness alone")
	})
}

// loadSkillForTest drives the same envelope a host sends when a skill loads, so a test that
// needs a briefed session gets one through the real path rather than by writing the marker
// file, which would let the two drift.
func loadSkillForTest(t *testing.T, ctx context.Context, dir, session string) {
	t.Helper()
	envelope := fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"session_id\":\"%s\",\"tool_name\":\"Skill\",\"tool_input\":{\"skill\":\"magus-multi-agent\"}}", session)
	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(envelope), &out,
		[]string{"--agent-name", "claude-code", "-o", "name"}))
	require.Equal(t, "defer\n", out.String(), "a skill load carries nothing to judge")
}

// TestHookCmd_RefusesSpawnBeforeTheBrief is spawn-before-skill end to end.
// With no workspace setting, a spawn is refused until the multi-agent skill
// loads, and passes after.
//
// The prompt is deliberately innocuous. The rule reads a marker file and never the prose,
// so nothing about the handed context should change the verdict either way.
func TestHookCmd_RefusesSpawnBeforeTheBrief(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(context.Background(), dir, "/repo/magus", "")
	envelope := `{"hook_event_name":"PreToolUse","session_id":"briefless","tool_name":"Task",` +
		`"tool_input":{"subagent_type":"Explore","prompt":"read the cache package"}}`

	t.Chdir(t.TempDir())
	installHostRows(t, "blind", types.Limits{})
	installHostRows(t, "claude-code", types.Limits{ObservesSkillLoads: true})
	// A host whose limits say no skill load is seen stands the rule down, which is what keeps
	// it from denying forever on a harness that reports no skill loads. Asserted first, because
	// it is the behaviour three of the four shipped harnesses get.
	var unobserved bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(envelope), &unobserved,
		[]string{"--agent-name", "blind", "-o", "name"}))
	assert.Equal(t, "defer\n", unobserved.String(),
		"a wiring that cannot report a skill load must not be held to having reported one")

	var refused bytes.Buffer
	var silent reportedExit
	require.ErrorAs(t, hookStdin(ctx, strings.NewReader(envelope), &refused,
		[]string{"--agent-name", "claude-code", "-o", "name"}), &silent)
	assert.Equal(t, guardDenyExitCode, silent.ExitCode())
	assert.Equal(t, "deny\n", refused.String())

	loadSkillForTest(t, ctx, dir, "briefless")

	var allowed bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader(envelope), &allowed,
		[]string{"--agent-name", "claude-code", "-o", "name"}))
	assert.Equal(t, "defer\n", allowed.String(), "the brief is read once per session, not per spawn")
}

// The host-call flags reach the guard: a worker's backgrounded magus run is refused, the
// same run in the foreground passes, and the call's timeout parses as milliseconds.
func TestShellReadsHowTheHostRunsTheCall(t *testing.T) {
	testkit.Isolate(t)
	root, cacheDir := testkit.NewRepository(t, nil).Root(), t.TempDir()
	row := jobtypes.Job{ID: "worker", Paths: jobtypes.Paths{Write: []string{"a.go"}}, Check: &jobtypes.LeaseCheck{Target: "test", Project: "."}, State: jobtypes.StateRunning, Registered: 1}
	_, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).Update(t.Context(), row.ID, func(cur *jobtypes.Job) { *cur = row })
	require.NoError(t, err)
	ctx := WithLocation(t.Context(), cacheDir, root, "")
	run := func(flags ...string) verdict.Judgment {
		var out bytes.Buffer
		args := append([]string{"--lease", row.ID, "-o", "json"}, flags...)
		_ = hookStdin(ctx, strings.NewReader("magus run test . --budget-wait 0"), &out, args)
		var v verdict.Judgment
		require.NoError(t, codec.Unmarshal(out.Bytes(), &v), out.String())
		return v
	}
	refused := run("--call-background")
	assert.Equal(t, []string{string(verdict.Deny), "worker-background-run"}, []string{string(refused.Decision), refused.Rule})
	assert.Equal(t, verdict.Defer, run("--call-timeout", "10m").Decision)
}

func TestShellDigestFlagsReachTheToolTimeRow(t *testing.T) {
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	ctx := WithLocation(t.Context(), cacheDir, root, "")

	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("Bash"), &out,
		[]string{"--duration", "420ms", "--call-digest", "c1", "--result-digest", "r1", "--event", "PostToolUse", "--session", "s1", "-o", "json"}))

	tr, err := trail.ForSession(ctx, cacheDir, "s1", 10)
	require.NoError(t, err)
	calls := tr.Calls
	require.Len(t, calls, 1)
	calls[0].At = time.Time{}
	assert.Equal(t, trail.SessionCall{
		Action: "Bash", ToolMs: 420,
		AgentFacts: types.AgentFacts{Thread: types.AgentThreadUnknown, ThreadFrom: types.ThreadInferred, CallDigest: "c1", ResultDigest: "r1"},
	}, calls[0])
}

func TestShellDurationRecordsTheToolAndPasses(t *testing.T) {
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	ctx := WithLocation(t.Context(), cacheDir, root, "")

	var out bytes.Buffer
	require.NoError(t, hookStdin(ctx, strings.NewReader("Bash"), &out,
		[]string{"--duration", "420ms", "--event", "PostToolUse", "--session", "s1", "-o", "json"}))
	var v verdict.Judgment
	require.NoError(t, codec.Unmarshal(out.Bytes(), &v), out.String())
	assert.Equal(t, verdict.Defer, v.Decision)

	events, err := loggedCalls(ctx, cacheDir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	event.Time, event.Origin, event.Workspace, event.Request, event.Response = time.Time{}, jobtypes.Origin{}, "", journal.Payload{}, journal.Payload{}
	assert.Equal(t, journal.Call{Kind: journal.CallKindAgentCommand, Name: "Bash", Outcome: journal.OutcomeOK, Duration: 420 * time.Millisecond, Preview: "observed"}, event)
}

// installHostRows writes the tool rows directory install writes for host under the working
// directory, with the given rows and limits.
func installHostRows(t *testing.T, host string, limits types.Limits, rows ...types.HarnessTool) {
	t.Helper()
	dir := filepath.Join("."+host, "magus", "tools")
	raw, err := codec.Marshal(limits)
	require.NoError(t, err)
	files := append(agent.ToolFiles(types.HarnessTools{Dir: dir, Tools: rows}, nil),
		types.AgentFile{Path: filepath.Join(dir, types.ToolHostFile), Content: host + "\n"},
		types.AgentFile{Path: filepath.Join(dir, types.ToolLimitsFile), Content: string(raw) + "\n"})
	for _, f := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(f.Path), 0o755))
		require.NoError(t, os.WriteFile(f.Path, []byte(f.Content), 0o644))
	}
}

// bindCheckout binds the checkout whose cache dir is cacheDir to the job id, as a marker.
func bindCheckout(t *testing.T, cacheDir, id string) {
	t.Helper()
	require.NoError(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir}).Bind(job.Caller{}, id))
}
