package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTable is a minimal engine.Table backed by insertion-ordered slices. It
// exists so tests can drive the pretty-printer and REPL result rendering
// without spinning up a real scripting engine.
type fakeTable struct {
	keys []engine.Value
	vals []engine.Value
}

func (t *fakeTable) IsNil() bool               { return false }
func (t *fakeTable) String() string            { return "table" }
func (t *fakeTable) AsString() (string, bool)  { return "", false }
func (t *fakeTable) AsNumber() (float64, bool) { return 0, false }
func (t *fakeTable) AsBool() bool              { return true }
func (t *fakeTable) AsTable() (engine.Table, bool) {
	return t, true
}
func (t *fakeTable) AsFunction() (engine.Value, bool) { return nil, false }

func (t *fakeTable) set(k, v engine.Value) {
	t.keys = append(t.keys, k)
	t.vals = append(t.vals, v)
}

func (t *fakeTable) RawSetString(key string, v engine.Value) {
	t.set(engine.StringValue(key), v)
}
func (t *fakeTable) RawGetString(string) engine.Value { return engine.NilValue }
func (t *fakeTable) RawSetInt(key int, v engine.Value) {
	t.set(engine.NumberValue(float64(key)), v)
}
func (t *fakeTable) RawGetInt(int) engine.Value { return engine.NilValue }
func (t *fakeTable) ForEach(fn func(k, v engine.Value)) {
	for i := range t.keys {
		fn(t.keys[i], t.vals[i])
	}
}
func (t *fakeTable) Len() int { return len(t.keys) }

// scriptDriver is a fake engine.ReplDriver whose behavior is fully scripted:
// each input line maps to a canned response so REPL loops are deterministic.
type scriptDriver struct {
	lang       string
	responses  map[string]driverResponse
	globals    map[string]engine.Value
	evalInputs []string // records every snippet EvalLine received
}

type driverResponse struct {
	vals       []engine.Value
	err        error
	incomplete bool // when true, err is reported as an incomplete-input error
}

func (d *scriptDriver) Language() string { return d.lang }

func (d *scriptDriver) EvalLine(snippet string) ([]engine.Value, error) {
	d.evalInputs = append(d.evalInputs, snippet)
	if r, ok := d.responses[snippet]; ok {
		return r.vals, r.err
	}
	return nil, nil
}

func (d *scriptDriver) IsIncomplete(err error) bool {
	for _, r := range d.responses {
		if r.incomplete && errors.Is(r.err, err) {
			return true
		}
	}
	return false
}

// LineDelta counts open vs close braces so the REPL buffers multi-line blocks.
func (d *scriptDriver) LineDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
}

func (d *scriptDriver) HostBindingNames() []string { return nil }

func (d *scriptDriver) UserGlobals() map[string]engine.Value { return d.globals }

// fakeSession is a scripted engine.Session that also advertises REPL drivers.
type fakeSession struct {
	drivers   []engine.ReplDriver
	globals   map[string]engine.Value
	doStrings []string
	doErr     error
}

func newFakeSession(drivers ...engine.ReplDriver) *fakeSession {
	return &fakeSession{drivers: drivers, globals: map[string]engine.Value{}}
}

func (s *fakeSession) Close() error { return nil }
func (s *fakeSession) SetGlobal(name string, v engine.Value) {
	s.globals[name] = v
}
func (s *fakeSession) GetGlobal(name string) engine.Value {
	if v, ok := s.globals[name]; ok {
		return v
	}
	return engine.NilValue
}
func (s *fakeSession) NewTable() engine.Table                  { return &fakeTable{} }
func (s *fakeSession) LoadString(string) (engine.Value, error) { return engine.NilValue, nil }
func (s *fakeSession) DoString(code string) error {
	s.doStrings = append(s.doStrings, code)
	return s.doErr
}
func (s *fakeSession) Call(engine.CallParams) error { return nil }

// Drivers satisfies engine.DriversProvider so replDrivers picks them up.
func (s *fakeSession) Drivers() []engine.ReplDriver { return s.drivers }

// runRepl drives Repl over the given input lines and returns stdout/stderr.
func runRepl(t *testing.T, sess engine.Session, opts ReplOptions, input string) (string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	opts.Stdin = strings.NewReader(input)
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	err := Repl(context.Background(), sess, opts)
	return stdout.String(), stderr.String(), err
}

func TestRepl_EOFExitsCleanly(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	out, errOut, err := runRepl(t, sess, ReplOptions{}, "")
	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Contains(t, out, "Type .exit to quit")
	// The prompt is printed once before the scan hits EOF.
	assert.Contains(t, out, "> ")
}

func TestRepl_BannerAndInjectedLocals(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	locals := map[string]engine.Value{"answer": engine.NumberValue(42)}
	out, _, err := runRepl(t, sess, ReplOptions{Banner: "hello banner", Locals: locals}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "hello banner")
	assert.Equal(t, engine.NumberValue(42), sess.GetGlobal("answer"))
}

func TestRepl_ExitMetaCommand(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	// .exit returns before evaluating anything; the trailing line is never read.
	_, _, err := runRepl(t, sess, ReplOptions{}, ".exit\nnever\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestRepl_QuitMetaCommand(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, err := runRepl(t, sess, ReplOptions{}, ".quit\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestRepl_HelpListsDrivers(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	out, _, err := runRepl(t, sess, ReplOptions{}, ".help\n")
	require.NoError(t, err)
	assert.Contains(t, out, ".buzz")
	assert.Contains(t, out, ".load <path>")
	assert.Contains(t, out, ".exit / .quit")
}

func TestRepl_EvaluatesAndPrettyPrintsResult(t *testing.T) {
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"1 + 1": {vals: []engine.Value{engine.NumberValue(2)}},
		},
	}
	sess := newFakeSession(drv)
	out, errOut, err := runRepl(t, sess, ReplOptions{}, "1 + 1\n")
	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Equal(t, []string{"1 + 1"}, drv.evalInputs)
	assert.Contains(t, out, "2")
}

func TestRepl_NilResultIsNotPrinted(t *testing.T) {
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"noop": {vals: []engine.Value{engine.NilValue}},
		},
	}
	sess := newFakeSession(drv)
	out, _, err := runRepl(t, sess, ReplOptions{}, "noop\n")
	require.NoError(t, err)
	// PrettyPrint renders a value on its own line terminated by a newline, so a
	// printed nil result would appear as the exact line "nil\n". Assert on that
	// rendered form rather than the bare substring "nil", which a banner or help
	// text containing those letters could otherwise trip.
	assert.NotContains(t, out, "nil\n")
}

func TestRepl_BlankAndCommentLinesSkipped(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, err := runRepl(t, sess, ReplOptions{}, "\n-- a comment\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestRepl_EvalErrorGoesToStderr(t *testing.T) {
	boom := &scriptError{"boom"}
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"bad": {err: boom},
		},
	}
	sess := newFakeSession(drv)
	_, errOut, err := runRepl(t, sess, ReplOptions{}, "bad\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "error: boom")
}

func TestRepl_BraceBufferingAccumulatesBlock(t *testing.T) {
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			// The full multi-line block is what EvalLine finally receives.
			"if x {\ndo()\n}": {vals: []engine.Value{engine.StringValue("done")}},
		},
	}
	sess := newFakeSession(drv)
	// The open brace pushes depth positive so the loop buffers instead of
	// evaluating; the closing "}" returns depth to 0 and the accumulated block runs.
	out, errOut, err := runRepl(t, sess, ReplOptions{}, "if x {\ndo()\n}\n")
	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Equal(t, []string{"if x {\ndo()\n}"}, drv.evalInputs)
	assert.Contains(t, out, "done")
}

func TestRepl_IncompleteErrorBuffersMoreInput(t *testing.T) {
	incompleteErr := &scriptError{"got EOF"}
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			// First snippet has balanced braces (LineDelta 0) so it is evaluated,
			// but reports incomplete; the REPL then keeps the pending buffer and
			// appends the next line before re-evaluating.
			"partial":       {err: incompleteErr, incomplete: true},
			"partial\nrest": {vals: []engine.Value{engine.StringValue("joined")}},
		},
	}
	sess := newFakeSession(drv)
	out, errOut, err := runRepl(t, sess, ReplOptions{}, "partial\nrest\n")
	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Contains(t, out, "joined")
}

func TestRepl_ContinuationPromptShown(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	out, _, err := runRepl(t, sess, ReplOptions{}, "open {\n")
	require.NoError(t, err)
	// The open brace pushes depth positive; the next prompt is the continuation form.
	assert.Contains(t, out, ">> ")
}

func TestRepl_NoDriverReportsError(t *testing.T) {
	sess := newFakeSession() // no drivers advertised
	_, errOut, err := runRepl(t, sess, ReplOptions{}, "anything\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "no REPL driver available")
}

func TestRepl_LoadExecutesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.buzz")
	require.NoError(t, os.WriteFile(path, []byte("print(1);"), 0o644))

	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, err := runRepl(t, sess, ReplOptions{}, ".load "+path+"\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"print(1);"}, sess.doStrings)
}

func TestRepl_LoadMissingFileReportsError(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, errOut, err := runRepl(t, sess, ReplOptions{}, ".load /no/such/file.buzz\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "error:")
	assert.Empty(t, sess.doStrings)
}

func TestRepl_ContextCancelledExitsCleanly(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr strings.Builder
	err := Repl(ctx, sess, ReplOptions{
		Stdin:  strings.NewReader("1 + 1\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

// scriptError is a trivial error carrying a fixed message for driver responses.
type scriptError struct{ msg string }

func (e *scriptError) Error() string { return e.msg }

// debugSession is a fakeSession that also implements engine.DebugReader so the
// pry frame/local/upvalue commands have something to introspect.
type debugSession struct {
	*fakeSession
	frames   []engine.Frame
	locals   map[string]engine.Value
	upvalues map[string]engine.Value
}

// stepperSession implements engine.Stepper so the .step/.next/.finish pry
// commands take their supported branches and return the stepping resume verbs.
type stepperSession struct {
	*fakeSession
}

func (s *stepperSession) SetStepHook(engine.StepMask, func(engine.StepEvent, engine.Frame)) {}
func (s *stepperSession) ClearStepHook()                                                    {}

func TestPry_StepCommandsSupported(t *testing.T) {
	// The three step verbs share one dispatch path; they differ only in the command
	// typed, the resume verb returned, and the banner line printed.
	cases := []struct {
		name       string
		command    string
		wantResume PryResume
		wantBanner string
	}{
		{"step", ".step\n", ResumeStep, "stepping into next line"},
		{"next", ".next\n", ResumeNext, "stepping over current line"},
		{"finish", ".finish\n", ResumeFinish, "running until current frame returns"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			isolatePryHistory(t)
			sess := &stepperSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"})}
			resume, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, tt.command)
			require.NoError(t, err)
			assert.Equal(t, tt.wantResume, resume)
			assert.Contains(t, out, tt.wantBanner)
		})
	}
}

func (s *debugSession) Frames() []engine.Frame { return s.frames }
func (s *debugSession) Locals(int) map[string]engine.Value {
	return s.locals
}
func (s *debugSession) Upvalues(int) map[string]engine.Value { return s.upvalues }
func (s *debugSession) CallDepth() int                       { return len(s.frames) }

// isolatePryHistory points history at a temp dir so Pry's OpenHistory(DefaultHistoryPath())
// never touches the real user state directory.
func isolatePryHistory(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// runPry drives Pry over input and returns the resume verb, stdout, stderr.
func runPry(t *testing.T, sess engine.Session, pctx PryContext, input string) (PryResume, string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	resume, err := Pry(context.Background(), sess, pctx, ReplOptions{
		Stdin:  strings.NewReader(input),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return resume, stdout.String(), stderr.String(), err
}

func TestPry_BannerAndContinue(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	pctx := PryContext{File: "prog.buzz", Line: 12, Func: "main"}
	resume, out, _, err := runPry(t, sess, pctx, ".continue\n")
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
	assert.Contains(t, out, "magus.pry at prog.buzz:12 in main")
	assert.Contains(t, out, "Type .help for pry commands")
}

func TestPry_BannerTopLevelNoLine(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	// No line and no func exercises the "(top level)" and location fallbacks.
	resume, out, _, err := runPry(t, sess, PryContext{File: "prog.buzz"}, ".exit\n")
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
	assert.Contains(t, out, "(top level)")
}

func TestPry_EOFResumesContinue(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	resume, _, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, "")
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
}

func TestPry_HelpIsConsumed(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	resume, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".help\n.exit\n")
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
	assert.Contains(t, out, "Pry commands:")
	assert.Contains(t, out, ".whereami")
	assert.Contains(t, out, ".pp <expr>")
}

func TestPry_StepNotSupportedResumesContinue(t *testing.T) {
	isolatePryHistory(t)
	// fakeSession does not implement engine.Stepper, so .step falls back.
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	resume, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".step\n")
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
	assert.Contains(t, out, "stepping not supported")
}

func TestPry_BacktraceWithFrames(t *testing.T) {
	isolatePryHistory(t)
	frames := []engine.Frame{
		{Source: "@a.buzz", ShortSrc: "a.buzz", CurrentLine: 3, Name: "inner"},
		{Source: "@b.buzz", ShortSrc: "b.buzz", CurrentLine: 9, Name: "outer"},
	}
	sess := &debugSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}), frames: frames}
	resume, out, _, err := runPry(t, sess, PryContext{File: "a.buzz"}, ".where\n.exit\n")
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
	assert.Contains(t, out, "#0 a.buzz:3 in inner")
	assert.Contains(t, out, "#1 b.buzz:9 in outer")
}

func TestPry_BacktraceNoFrames(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".backtrace\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "no frames")
}

func TestPry_LocalsListed(t *testing.T) {
	isolatePryHistory(t)
	locals := map[string]engine.Value{
		"beta":  engine.NumberValue(2),
		"alpha": engine.StringValue("x"),
	}
	sess := &debugSession{
		fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}),
		frames:      []engine.Frame{{Source: "@a.buzz", ShortSrc: "a.buzz", CurrentLine: 1, Name: "f"}},
		locals:      locals,
	}
	_, out, _, err := runPry(t, sess, PryContext{File: "a.buzz"}, ".locals\n.exit\n")
	require.NoError(t, err)
	// Assert both are present before comparing positions: strings.Index returns -1
	// for a missing substring, and -1 < someIndex would let a dropped local pass the
	// ordering check silently.
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "beta")
	// Sorted: alpha before beta.
	assert.Less(t, strings.Index(out, "alpha"), strings.Index(out, "beta"))
}

func TestPry_LocalsWithUpvalues(t *testing.T) {
	isolatePryHistory(t)
	sess := &debugSession{
		fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}),
		frames:      []engine.Frame{{Source: "@a.buzz", ShortSrc: "a.buzz", CurrentLine: 1, Name: "f"}},
		locals:      map[string]engine.Value{"loc": engine.NumberValue(1)},
		upvalues:    map[string]engine.Value{"cap": engine.NumberValue(9)},
	}
	_, out, _, err := runPry(t, sess, PryContext{File: "a.buzz"}, ".locals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "upvalues:")
	assert.Contains(t, out, "cap")
}

func TestPry_LocalsNoDebugReader(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".locals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "engine does not expose locals")
}

func TestPry_GlobalsListed(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{
		lang:    "buzz",
		globals: map[string]engine.Value{"gvar": engine.NumberValue(7)},
	}
	sess := newFakeSession(drv)
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".globals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "gvar")
}

func TestPry_GlobalsEmpty(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{lang: "buzz", globals: map[string]engine.Value{}}
	sess := newFakeSession(drv)
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".globals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "no user globals")
}

func TestPry_GlobalsNilMapNotAvailable(t *testing.T) {
	isolatePryHistory(t)
	// A nil globals map signals the engine cannot list globals.
	drv := &scriptDriver{lang: "buzz", globals: nil}
	sess := newFakeSession(drv)
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".globals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "globals not available")
}

func TestPry_UpDownFrameNavigation(t *testing.T) {
	isolatePryHistory(t)
	frames := []engine.Frame{
		{Source: "@a.buzz", ShortSrc: "a.buzz", CurrentLine: 3, Name: "inner"},
		{Source: "@b.buzz", ShortSrc: "b.buzz", CurrentLine: 9, Name: "outer"},
	}
	sess := &debugSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}), frames: frames}
	// .down at innermost is a no-op; .up moves to outer; .up again is capped.
	_, out, _, err := runPry(t, sess, PryContext{File: "a.buzz"}, ".down\n.up\n.up\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "already at innermost frame")
	assert.Contains(t, out, "#1 b.buzz:9 in outer")
	assert.Contains(t, out, "already at outermost frame")
}

func TestPry_PromptReflectsSelectedFrame(t *testing.T) {
	isolatePryHistory(t)
	frames := []engine.Frame{
		{Source: "@a.buzz", ShortSrc: "a.buzz", CurrentLine: 3, Name: "inner"},
		{Source: "@b.buzz", ShortSrc: "b.buzz", CurrentLine: 9, Name: "outer"},
	}
	sess := &debugSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}), frames: frames}
	_, out, _, err := runPry(t, sess, PryContext{File: "a.buzz"}, ".up\n.exit\n")
	require.NoError(t, err)
	// After moving up one frame the prompt carries the frame index.
	assert.Contains(t, out, "pry[1]>")
}

func TestPry_PpEvaluatesExpression(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"1 + 2": {vals: []engine.Value{engine.NumberValue(3)}},
		},
	}
	sess := newFakeSession(drv)
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".pp 1 + 2\n.exit\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"1 + 2"}, drv.evalInputs)
	assert.Contains(t, out, "3")
}

func TestPry_PpEvaluationError(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"bad": {err: &scriptError{"kaboom"}},
		},
	}
	sess := newFakeSession(drv)
	_, _, errOut, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".pp bad\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "error: kaboom")
}

func TestPry_HistoryCommand(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"x": {vals: []engine.Value{engine.NumberValue(1)}},
		},
	}
	sess := newFakeSession(drv)
	// Evaluate x (recorded to history), then request the history listing.
	_, out, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, "x\n.history\n.exit\n")
	require.NoError(t, err)
	// Assert on PrintHistory's numbered-listing form ("%4d: %s"), not the bare
	// echoed eval line "x": the recall index proves .history actually rendered.
	assert.Contains(t, out, "   1: x")
}

func TestPry_LoadExecutesFile(t *testing.T) {
	isolatePryHistory(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.buzz")
	require.NoError(t, os.WriteFile(path, []byte("print(2);"), 0o644))

	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	_, _, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, ".load "+path+"\n.exit\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"print(2);"}, sess.doStrings)
}

func TestPry_EvalError(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{
		lang: "buzz",
		responses: map[string]driverResponse{
			"oops": {err: &scriptError{"nope"}},
		},
	}
	sess := newFakeSession(drv)
	_, _, errOut, err := runPry(t, sess, PryContext{File: "p.buzz"}, "oops\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "error: nope")
}

func TestPry_BlankAndCommentLinesSkipped(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, _, err := runPry(t, sess, PryContext{File: "p.buzz"}, "\n-- note\n.exit\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestPry_ContextCancelledResumesContinue(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr strings.Builder
	resume, err := Pry(ctx, sess, PryContext{File: "p.buzz"}, ReplOptions{
		Stdin:  strings.NewReader("x\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	require.NoError(t, err)
	assert.Equal(t, ResumeContinue, resume)
}

func TestPry_WhereamiWithSource(t *testing.T) {
	isolatePryHistory(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "src.buzz")
	require.NoError(t, os.WriteFile(path, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644))

	frames := []engine.Frame{{Source: path, ShortSrc: path, CurrentLine: 3, Name: "f"}}
	sess := &debugSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}), frames: frames}
	_, out, _, err := runPry(t, sess, PryContext{File: path, Line: 3, Func: "f"}, ".whereami\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "From:")
	assert.Contains(t, out, "l3")
}

// TestReplStateNamesTheContinuationDepth covers the footer line's whole point. A
// ">>" continuation prompt is indistinguishable from a hung process, which is what
// makes people kill a REPL mid-expression; the footer says how deep it is and
// therefore how many closers are owed.
func TestReplStateNamesTheContinuationDepth(t *testing.T) {
	assert.Equal(t, "magus buzz  |  buzz  |  /w  |  .help", replState("buzz", "/w", 0, false))
	assert.Equal(t, "magus buzz  |  buzz  |  /w  |  continuing (depth 2)  |  .help", replState("buzz", "/w", 2, false))
	assert.Equal(t, "magus buzz  |  buzz  |  /w  |  continuing  |  .help", replState("buzz", "/w", 0, true),
		"buffered input with depth 0 (an unterminated statement) still reports continuing")
	assert.Equal(t, "magus buzz  |  .help", replState("", "", 0, false),
		"an unknown language and cwd drop out rather than printing empty separators")
}

// TestShortenPathKeepsTheIdentifyingTail pins which half survives clipping. A
// footer clipped from the right would show the same home-directory prefix for every
// session; the tail is what names the project.
func TestShortenPathKeepsTheIdentifyingTail(t *testing.T) {
	short := "/home/e/magus"
	assert.Equal(t, short, shortenPath(short), "a path that fits is untouched")

	long := "/Users/someone/very/deeply/nested/checkout/path/magus/console"
	got := shortenPath(long)
	assert.True(t, strings.HasPrefix(got, "..."), "clipping is marked")
	assert.True(t, strings.HasSuffix(got, "/magus/console"), "the tail identifies the project")
	assert.NotContains(t, got, "/Users/someone", "the uninformative prefix is what goes")
}

// TestReplFooterIsInertOffATTY keeps a piped or scripted session byte-identical to
// one with no footer at all: no escape sequences, no status line, no branch at the
// call site.
func TestReplFooterIsInertOffATTY(t *testing.T) {
	var buf bytes.Buffer // a plain buffer has no Fd, so Region reports disabled
	f := newReplFooter(&buf)
	f.paint(replState("buzz", "/w", 1, true))
	f.release()
	assert.Empty(t, buf.String(), "a non-TTY REPL emits nothing extra")
}

// fakeDriver is a ReplDriver that only answers UserGlobals, which is all the
// completer asks of it.
type fakeDriver struct {
	engine.ReplDriver
	globals map[string]engine.Value
}

func (d fakeDriver) UserGlobals() map[string]engine.Value { return d.globals }

func testCompleter(globals []string, candidates []string) (*replCompleter, *bytes.Buffer) {
	g := map[string]engine.Value{}
	for _, n := range globals {
		g[n] = engine.NilValue
	}
	var out bytes.Buffer
	return &replCompleter{
		meta:       []string{".exit", ".quit", ".help", ".load", ".buzz"},
		driver:     func() engine.ReplDriver { return fakeDriver{globals: g} },
		candidates: func() []string { return candidates },
		out:        &out,
	}, &out
}

// TestCompleteOnlyActsOnTab keeps ordinary typing untouched: the callback fires on
// EVERY keypress, so declining anything but Tab is what stops it rewriting the line
// as the user types.
func TestCompleteOnlyActsOnTab(t *testing.T) {
	c, _ := testCompleter([]string{"total"}, nil)
	_, _, ok := c.complete("tot", 3, 'a')
	assert.False(t, ok, "a normal keypress must be processed normally")
	_, _, ok = c.complete("tot", 3, '\t')
	assert.True(t, ok, "Tab completes")
}

// TestCompleteInsertsASingleMatch covers the common case, including that the
// completion is spliced at the word rather than appended to the line.
func TestCompleteInsertsASingleMatch(t *testing.T) {
	c, _ := testCompleter(nil, []string{"buf-generate"})
	line, pos, ok := c.complete("run buf-g", 9, '\t')
	require.True(t, ok)
	assert.Equal(t, "run buf-generate", line)
	assert.Equal(t, len("run buf-generate"), pos)
}

// TestCompleteInsertsTheCommonPrefixThenLists is the ambiguous case, and the two
// presses are deliberately different: the first makes what progress it can, and only
// when there is none left does it spend the reader's screen on a list.
func TestCompleteInsertsTheCommonPrefixThenLists(t *testing.T) {
	c, out := testCompleter(nil, []string{"buf-build", "buf-lint", "buf-format"})

	line, pos, ok := c.complete("bu", 2, '\t')
	require.True(t, ok, "the shared prefix is progress")
	assert.Equal(t, "buf-", line)
	assert.Equal(t, 4, pos)
	assert.Empty(t, out.String(), "no list while there is still prefix to insert")

	_, _, ok = c.complete(line, pos, '\t')
	assert.False(t, ok, "nothing more to insert, so the line is unchanged")
	listed := out.String()
	for _, want := range []string{"buf-build", "buf-lint", "buf-format"} {
		assert.Contains(t, listed, want, "the second Tab shows the alternatives")
	}
}

// TestCompleteScopesMetaCommandsToTheDotPrefix keeps the two namespaces apart: a
// leading dot can only be a meta command, and mixing every global and target into
// that list would bury the five things that are actually valid there.
func TestCompleteScopesMetaCommandsToTheDotPrefix(t *testing.T) {
	c, _ := testCompleter([]string{"exitCode"}, []string{"export-thing"})

	line, _, ok := c.complete(".e", 2, '\t')
	require.True(t, ok)
	assert.Equal(t, ".exit", line, "only the meta command matches, not exitCode or export-thing")

	c2, _ := testCompleter([]string{"exitCode"}, nil)
	line, _, ok = c2.complete("exi", 3, '\t')
	require.True(t, ok)
	assert.Equal(t, "exitCode", line, "and without the dot, the session global is what completes")
}

// TestCompleteFindsNothingQuietly: an unmatched prefix must leave the line alone
// rather than blanking it, which is what returning ok=true with an empty insert
// would do.
func TestCompleteFindsNothingQuietly(t *testing.T) {
	c, out := testCompleter([]string{"total"}, []string{"build"})
	line, pos, ok := c.complete("zzz", 3, '\t')
	assert.False(t, ok)
	assert.Empty(t, line)
	assert.Zero(t, pos)
	assert.Empty(t, out.String())
}

// TestWordStartTreatsBuzzMemberAccessAsOneWord pins the tokenizer against Buzz's
// own syntax: a member access is a dot and a namespace is a backslash, so neither
// may end a word - completing "fs.wri" has to see "fs." to know what it is.
func TestWordStartTreatsBuzzMemberAccessAsOneWord(t *testing.T) {
	assert.Equal(t, 0, wordStart("fs.wri", 6), "a dotted member is one word")
	assert.Equal(t, 0, wordStart(`magus\Con`, 9), "a namespace is one word")
	assert.Equal(t, 4, wordStart("run buf-", 8), "a space starts a new word")
	assert.Equal(t, 6, wordStart(`join("a`, 7), "the quote starts the word, so only `a` completes")
}

// TestCompleteDedupesAcrossSources: a name can be both a session global and a
// workspace target, and offering it twice makes the list look wrong.
func TestCompleteDedupesAcrossSources(t *testing.T) {
	c, _ := testCompleter([]string{"build"}, []string{"build", "build"})
	assert.Equal(t, []string{"build"}, c.matching("bui"))
}
