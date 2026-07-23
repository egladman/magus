package interp_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
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

// runRepl drives interp.Repl over the given input lines and returns stdout/stderr.
func runRepl(t *testing.T, sess engine.Session, opts interp.ReplOptions, input string) (string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	opts.Stdin = strings.NewReader(input)
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	err := interp.Repl(context.Background(), sess, opts)
	return stdout.String(), stderr.String(), err
}

func TestRepl_EOFExitsCleanly(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	out, errOut, err := runRepl(t, sess, interp.ReplOptions{}, "")
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
	out, _, err := runRepl(t, sess, interp.ReplOptions{Banner: "hello banner", Locals: locals}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "hello banner")
	assert.Equal(t, engine.NumberValue(42), sess.GetGlobal("answer"))
}

func TestRepl_ExitMetaCommand(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	// .exit returns before evaluating anything; the trailing line is never read.
	_, _, err := runRepl(t, sess, interp.ReplOptions{}, ".exit\nnever\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestRepl_QuitMetaCommand(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, err := runRepl(t, sess, interp.ReplOptions{}, ".quit\n")
	require.NoError(t, err)
	assert.Empty(t, drv.evalInputs)
}

func TestRepl_HelpListsDrivers(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	out, _, err := runRepl(t, sess, interp.ReplOptions{}, ".help\n")
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
	out, errOut, err := runRepl(t, sess, interp.ReplOptions{}, "1 + 1\n")
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
	out, _, err := runRepl(t, sess, interp.ReplOptions{}, "noop\n")
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
	_, _, err := runRepl(t, sess, interp.ReplOptions{}, "\n-- a comment\n")
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
	_, errOut, err := runRepl(t, sess, interp.ReplOptions{}, "bad\n")
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
	out, errOut, err := runRepl(t, sess, interp.ReplOptions{}, "if x {\ndo()\n}\n")
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
	out, errOut, err := runRepl(t, sess, interp.ReplOptions{}, "partial\nrest\n")
	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Contains(t, out, "joined")
}

func TestRepl_ContinuationPromptShown(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	out, _, err := runRepl(t, sess, interp.ReplOptions{}, "open {\n")
	require.NoError(t, err)
	// The open brace pushes depth positive; the next prompt is the continuation form.
	assert.Contains(t, out, ">> ")
}

func TestRepl_NoDriverReportsError(t *testing.T) {
	sess := newFakeSession() // no drivers advertised
	_, errOut, err := runRepl(t, sess, interp.ReplOptions{}, "anything\n")
	require.NoError(t, err)
	assert.Contains(t, errOut, "no REPL driver available")
}

func TestRepl_LoadExecutesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.buzz")
	require.NoError(t, os.WriteFile(path, []byte("print(1);"), 0o644))

	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, _, err := runRepl(t, sess, interp.ReplOptions{}, ".load "+path+"\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"print(1);"}, sess.doStrings)
}

func TestRepl_LoadMissingFileReportsError(t *testing.T) {
	drv := &scriptDriver{lang: "buzz"}
	sess := newFakeSession(drv)
	_, errOut, err := runRepl(t, sess, interp.ReplOptions{}, ".load /no/such/file.buzz\n")
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
	err := interp.Repl(ctx, sess, interp.ReplOptions{
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
