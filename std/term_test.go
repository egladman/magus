//go:build !wasm

package std

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/pick"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The whole point of this module's design: a prompt must be impossible to reach
// when nothing can answer it. Under `go test` stdin is not a terminal, so these
// exercise the real non-interactive path rather than a simulated one.

func TestTermPickRaisesWithoutATerminal(t *testing.T) {
	_, err := TermPick(context.Background(), []string{"a", "b"}, "pick one", "", 0, 0, nil)
	require.Error(t, err, "pick must never block waiting for input that cannot arrive")
	// The message has to name the guard, not just the condition: someone hitting
	// this in CI needs to know what to write.
	assert.Contains(t, err.Error(), "isInteractive")
	assert.Contains(t, err.Error(), "terminals")
}

func TestTermPickRejectsAnEmptyList(t *testing.T) {
	_, err := TermPick(context.Background(), nil, "", "", 0, 0, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no items")
}

func TestTermPickIsSkippedInRecordMode(t *testing.T) {
	// A dry run must never block on a human, and must not raise either: it
	// reports a plausible answer the way fs.temp_dir names a path it did not
	// create.
	idx, err := TermPick(types.WithTrace(context.Background()), []string{"a", "b"}, "", "", 0, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)

	// Even with no items, because the record pass is not asserting a real choice.
	idx, err = TermPick(types.WithTrace(context.Background()), nil, "", "", 0, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)
}

func TestTermIsInteractiveIsFalseUnderTest(t *testing.T) {
	got, err := TermIsInteractive(context.Background())
	require.NoError(t, err)
	assert.False(t, got, "the test harness is not a terminal, so this must report false")
}

func TestTermColorizeIsPassThroughWithoutATerminal(t *testing.T) {
	// Escape codes leaking into a CI log is the bug this prevents, so the
	// pass-through is the behaviour under test, not an implementation detail.
	got, err := TermColorize(context.Background(), "hello", string(types.TermRed))
	require.NoError(t, err)
	assert.Equal(t, "hello", got, "no terminal means no escape codes")

	// An unset style is pass-through regardless, so a computed style needs no branch.
	got, err = TermColorize(context.Background(), "hello", "")
	require.NoError(t, err)
	assert.Equal(t, "hello", got)
}

func TestTermSizeIsZeroWithoutATerminal(t *testing.T) {
	// Zero rather than an error: "there is no size" is an ordinary answer for a
	// pipe, and a caller laying out a line should not need a try/catch to ask.
	got, err := TermSize(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TermSize{}, got)
}

func TestTermClearScreenIsANoOpWithoutATerminal(t *testing.T) {
	// So a watch loop needs no guard around its repaint.
	require.NoError(t, TermClearScreen(context.Background()))
}

func TestTermStyleCasesAreTheRenderedCodes(t *testing.T) {
	// The enum names magus's own palette rather than inventing one; these are the
	// SGR codes internal/interactive/tty already emits.
	assert.Equal(t, types.TermStyle("1"), types.TermBold)
	assert.Equal(t, types.TermStyle("2;32"), types.TermDimGreen)
	assert.Equal(t, types.TermStyle("1;32"), types.TermBrightGreen)
	assert.True(t, types.TermRed.Valid())
	assert.False(t, types.TermStyle("nope").Valid())
	// The zero value is valid and means "no styling".
	assert.True(t, types.TermStyle("").Valid())
}

// TestTermWantsColorIsFalseWithoutATerminal: the test binary's stderr is a pipe,
// so styled output is off and colorize is pass-through. A caller reads this only
// to make a WIDER rendering choice: a box-drawing table versus a plain one.
func TestTermWantsColorIsFalseWithoutATerminal(t *testing.T) {
	got, err := TermWantsColor(context.Background())
	require.NoError(t, err)
	assert.False(t, got)
}

// TestTermNotifyNeverRaises is the whole contract: a notification is a VIEW, so
// every way it can fail to be shown (no terminal, no room, an empty message, a
// recording pass) is a silent drop. Making a magusfile guard it would be a tax
// paid at every call site for a condition no author can act on.
func TestTermNotifyNeverRaises(t *testing.T) {
	for _, tc := range []struct {
		name      string
		recording bool
		message   string
		level     string
		ttlMs     int
	}{
		{name: "default ttl", message: "built", level: "info"},
		{name: "explicit ttl", message: "built", level: "warn", ttlMs: 250},
		{name: "a negative ttl pins the notification", message: "built", level: "error", ttlMs: -1},
		{name: "an unknown level is not an error", message: "built", level: "shouting"},
		{name: "an empty message is a no-op"},
		{name: "a recording pass paints nothing", recording: true, message: "built", level: "info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.recording {
				ctx = types.WithTrace(ctx)
			}
			require.NoError(t, TermNotify(ctx, tc.message, tc.level, tc.ttlMs))
		})
	}
}

// TestNotifyStyle maps a severity onto the palette magus already renders with.
// LogLevel rather than a term-specific enum: a notification's severity is the
// same question log.at asks, and two spellings of "warn" would be one too many.
func TestNotifyStyle(t *testing.T) {
	for _, tc := range []struct {
		level types.LogLevel
		want  termkit.SGR
	}{
		{types.LogError, termkit.SGRRed},
		{types.LogWarn, termkit.SGRYellow},
		{types.LogTrace, termkit.SGRDim},
		{types.LogDebug, termkit.SGRDim},
		{types.LogInfo, ""},
		{"", ""},
		{"shouting", ""},
	} {
		assert.Equalf(t, tc.want, notifyStyle(tc.level), "notifyStyle(%q)", string(tc.level))
	}
}

// TermPromptsSuite covers confirm, pickMany, pick's query and spin. Under `go
// test` stdin is not a terminal, so the refusals exercise the real
// non-interactive path rather than a simulated one.
type TermPromptsSuite struct{ suite.Suite }

func TestTermPromptsSuite(t *testing.T) { suite.Run(t, new(TermPromptsSuite)) }

func (s *TermPromptsSuite) TestConfirmRaisesWithoutATerminal() {
	_, err := TermConfirm(context.Background(), "remove 3 worktrees?")
	s.Require().Error(err)
	s.Contains(err.Error(), "term.confirm")
	s.Contains(err.Error(), "isInteractive", "the message names the guard to write")
}

func (s *TermPromptsSuite) TestConfirmDeclinesInRecordMode() {
	yes, err := TermConfirm(types.WithTrace(context.Background()), "push?")
	s.Require().NoError(err)
	s.False(yes, "a dry run never takes the step a confirm guards")
}

func (s *TermPromptsSuite) TestConfirmRefusesAnEmptyQuestion() {
	_, err := TermConfirm(context.Background(), "")
	s.Require().Error(err)
	s.Contains(err.Error(), "no question")
}

func (s *TermPromptsSuite) TestConfirmHighlightsNoFirst() {
	s.Equal([]string{"no", "yes"}, confirmAnswers, "Enter alone must decline")
}

func (s *TermPromptsSuite) TestPickManyRaisesWithoutATerminal() {
	_, err := TermPickMany(context.Background(), []string{"a", "b"}, "branches", "", nil, 0)
	s.Require().Error(err)
	s.Contains(err.Error(), "term.pickMany")
	s.Contains(err.Error(), "isInteractive")
}

func (s *TermPromptsSuite) TestPickManyMarksNothingInRecordMode() {
	got, err := TermPickMany(types.WithTrace(context.Background()), []string{"a", "b"}, "", "", []string{"a"}, 0)
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *TermPromptsSuite) TestPickManyRejectsAnEmptyList() {
	_, err := TermPickMany(context.Background(), nil, "", "", nil, 0)
	s.Require().Error(err)
	s.Contains(err.Error(), "no items")
}

func (s *TermPromptsSuite) TestPickManyRejectsADuplicateItem() {
	_, err := TermPickMany(context.Background(), []string{"a", "b", "a"}, "", "", nil, 0)
	s.Require().Error(err)
	s.Contains(err.Error(), `"a" is listed twice`)
}

func (s *TermPromptsSuite) TestPickManyRejectsAMarkThatIsNotAnItem() {
	_, err := TermPickMany(context.Background(), []string{"a", "b"}, "", "", []string{"c"}, 0)
	s.Require().Error(err)
	s.Contains(err.Error(), `marked names "c"`)
}

func (s *TermPromptsSuite) TestMarkedIndicesFollowItems() {
	got, err := termMarkedIndices([]string{"a", "b", "c"}, []string{"c", "a"})
	s.Require().NoError(err)
	s.Equal([]int{2, 0}, got)
}

func (s *TermPromptsSuite) TestPickWithAQueryNeedsNoItems() {
	query := func(context.Context, string) (any, error) { return []any{"x"}, nil }
	_, err := TermPick(context.Background(), nil, "symbol", "", 0, 0, query)
	s.Require().Error(err, "still no terminal")
	s.NotContains(err.Error(), "no items", "a live query supplies the items")
	s.Contains(err.Error(), "isInteractive")
}

func (s *TermPromptsSuite) TestQueryResultAcceptsAListOfStrings() {
	got, err := termQueryResult([]any{"a", "b"})
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, got)

	got, err = termQueryResult([]string{"c"})
	s.Require().NoError(err)
	s.Equal([]string{"c"}, got)
}

func (s *TermPromptsSuite) TestQueryResultNamesAWrongShape() {
	_, err := termQueryResult("a")
	s.Require().Error(err)
	s.Contains(err.Error(), "want a list of strings")

	_, err = termQueryResult([]any{"a", int64(2)})
	s.Require().Error(err)
	s.Contains(err.Error(), "at index 1")
}

func (s *TermPromptsSuite) TestPromptErrorNamesACancel() {
	s.Equal("term.confirm: cancelled", termPromptError("term.confirm", pick.ErrAborted).Error())
	s.Equal("term.pick: wrapped", termPromptError("term.pick", errors.New("wrapped")).Error())
}

func (s *TermPromptsSuite) TestSpinRaisesWithoutATerminalAndDoesNotRun() {
	ran := false
	_, err := TermSpin(context.Background(), "surveying", func(context.Context) (any, error) {
		ran = true
		return nil, nil
	})
	s.Require().Error(err)
	s.Contains(err.Error(), "term.spin")
	s.Contains(err.Error(), "isInteractive")
	s.False(ran, "the work must not start behind a refusal")
}

func (s *TermPromptsSuite) TestSpinRunsTheWorkInRecordMode() {
	got, err := TermSpin(types.WithTrace(context.Background()), "surveying", func(context.Context) (any, error) {
		return "done", nil
	})
	s.Require().NoError(err)
	s.Equal("done", got)
}

func (s *TermPromptsSuite) TestSpinRefusesNoFunction() {
	_, err := TermSpin(context.Background(), "surveying", nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "no function")
}
