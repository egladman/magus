package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		wantResume interp.PryResume
		wantBanner string
	}{
		{"step", ".step\n", interp.ResumeStep, "stepping into next line"},
		{"next", ".next\n", interp.ResumeNext, "stepping over current line"},
		{"finish", ".finish\n", interp.ResumeFinish, "running until current frame returns"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			isolatePryHistory(t)
			sess := &stepperSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"})}
			resume, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, tt.command)
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

// isolatePryHistory points history at a temp dir so Pry's Open(DefaultPath())
// never touches the real user state directory.
func isolatePryHistory(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// runPry drives interp.Pry over input and returns the resume verb, stdout, stderr.
func runPry(t *testing.T, sess engine.Session, pctx interp.PryContext, input string) (interp.PryResume, string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	resume, err := interp.Pry(context.Background(), sess, pctx, interp.ReplOptions{
		Stdin:  strings.NewReader(input),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return resume, stdout.String(), stderr.String(), err
}

func TestPry_BannerAndContinue(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	pctx := interp.PryContext{File: "prog.buzz", Line: 12, Func: "main"}
	resume, out, _, err := runPry(t, sess, pctx, ".continue\n")
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
	assert.Contains(t, out, "magus.pry at prog.buzz:12 in main")
	assert.Contains(t, out, "Type .help for pry commands")
}

func TestPry_BannerTopLevelNoLine(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	// No line and no func exercises the "(top level)" and location fallbacks.
	resume, out, _, err := runPry(t, sess, interp.PryContext{File: "prog.buzz"}, ".exit\n")
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
	assert.Contains(t, out, "(top level)")
}

func TestPry_EOFResumesContinue(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	resume, _, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, "")
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
}

func TestPry_HelpIsConsumed(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	resume, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".help\n.exit\n")
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
	assert.Contains(t, out, "Pry commands:")
	assert.Contains(t, out, ".whereami")
	assert.Contains(t, out, ".pp <expr>")
}

func TestPry_StepNotSupportedResumesContinue(t *testing.T) {
	isolatePryHistory(t)
	// fakeSession does not implement engine.Stepper, so .step falls back.
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	resume, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".step\n")
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
	assert.Contains(t, out, "stepping not supported")
}

func TestPry_BacktraceWithFrames(t *testing.T) {
	isolatePryHistory(t)
	frames := []engine.Frame{
		{Source: "@a.buzz", ShortSrc: "a.buzz", CurrentLine: 3, Name: "inner"},
		{Source: "@b.buzz", ShortSrc: "b.buzz", CurrentLine: 9, Name: "outer"},
	}
	sess := &debugSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}), frames: frames}
	resume, out, _, err := runPry(t, sess, interp.PryContext{File: "a.buzz"}, ".where\n.exit\n")
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
	assert.Contains(t, out, "#0 a.buzz:3 in inner")
	assert.Contains(t, out, "#1 b.buzz:9 in outer")
}

func TestPry_BacktraceNoFrames(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".backtrace\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "a.buzz"}, ".locals\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "a.buzz"}, ".locals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "upvalues:")
	assert.Contains(t, out, "cap")
}

func TestPry_LocalsNoDebugReader(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".locals\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".globals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "gvar")
}

func TestPry_GlobalsEmpty(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{lang: "buzz", globals: map[string]engine.Value{}}
	sess := newFakeSession(drv)
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".globals\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "no user globals")
}

func TestPry_GlobalsNilMapNotAvailable(t *testing.T) {
	isolatePryHistory(t)
	// A nil globals map signals the engine cannot list globals.
	drv := &scriptDriver{lang: "buzz", globals: nil}
	sess := newFakeSession(drv)
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".globals\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "a.buzz"}, ".down\n.up\n.up\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "a.buzz"}, ".up\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".pp 1 + 2\n.exit\n")
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
	_, _, errOut, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".pp bad\n.exit\n")
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
	_, out, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, "x\n.history\n.exit\n")
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
	_, _, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, ".load "+path+"\n.exit\n")
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
	_, _, errOut, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, "oops\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "error: nope")
}

func TestPry_BlankAndCommentLinesSkipped(t *testing.T) {
	isolatePryHistory(t)
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, _, err := runPry(t, sess, interp.PryContext{File: "p.buzz"}, "\n-- note\n.exit\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestPry_ContextCancelledResumesContinue(t *testing.T) {
	isolatePryHistory(t)
	sess := newFakeSession(&scriptDriver{lang: "buzz"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr strings.Builder
	resume, err := interp.Pry(ctx, sess, interp.PryContext{File: "p.buzz"}, interp.ReplOptions{
		Stdin:  strings.NewReader("x\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	require.NoError(t, err)
	assert.Equal(t, interp.ResumeContinue, resume)
}

func TestPry_WhereamiWithSource(t *testing.T) {
	isolatePryHistory(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "src.buzz")
	require.NoError(t, os.WriteFile(path, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644))

	frames := []engine.Frame{{Source: path, ShortSrc: path, CurrentLine: 3, Name: "f"}}
	sess := &debugSession{fakeSession: newFakeSession(&scriptDriver{lang: "buzz"}), frames: frames}
	_, out, _, err := runPry(t, sess, interp.PryContext{File: path, Line: 3, Func: "f"}, ".whereami\n.exit\n")
	require.NoError(t, err)
	assert.Contains(t, out, "From:")
	assert.Contains(t, out, "l3")
}
