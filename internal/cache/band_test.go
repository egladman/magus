package cache

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/interactive/screen"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

const bandScreenRows = 30

type sizedProbe struct{ width, height int }

func (sizedProbe) IsTerminal(uintptr) bool { return true }

func (p sizedProbe) Size(uintptr) (int, int, error) { return p.width, p.height, nil }

// tapScreen is a screen that also keeps the bytes written to it.
type tapScreen struct {
	*screen.Screen
	log bytes.Buffer
}

func (t *tapScreen) Write(p []byte) (int, error) {
	t.log.Write(p)
	return t.Screen.Write(p)
}

type BandSuite struct {
	suite.Suite
	now time.Time
}

func TestBandSuite(t *testing.T) { suite.Run(t, new(BandSuite)) }

func (s *BandSuite) SetupTest() {
	s.T().Setenv("TERM", "xterm-256color")
	s.T().Setenv("NO_COLOR", "")
	s.now = time.Unix(0, 0)
}

func (s *BandSuite) handler(width int) (*screen.Screen, *PrettyHandler) {
	sc := screen.New(width, bandScreenRows)
	return sc, s.handlerOn(sc, width)
}

func (s *BandSuite) handlerOn(w interface {
	Write([]byte) (int, error)
	Fd() uintptr
}, width int) *PrettyHandler {
	h := newPrettyHandler(w, slog.LevelInfo, sizedProbe{width, bandScreenRows})
	h.tickEvery = 0
	h.now = func() time.Time { return s.now }
	return h
}

func (s *BandSuite) handle(h *PrettyHandler, msg string, attrs ...slog.Attr) {
	s.Require().NoError(h.Handle(context.Background(), buildRecord(msg, attrs...)))
}

func (s *BandSuite) fail(h *PrettyHandler, project, target string, dur time.Duration) {
	s.handle(h, "cache.error", slog.String("project", project), slog.String("target", target),
		slog.Int64("duration", int64(dur)), slog.String("error", "exit status 1"),
		slog.String("ref", "out-"+target))
}

func (s *BandSuite) stepEvent(h *PrettyHandler, project, target string, state StepState) {
	s.handle(h, stepEvent, slog.String("project", project), slog.String("target", target),
		slog.String("state", string(state)))
}

func (s *BandSuite) pool(h *PrettyHandler, running, queued int) {
	s.handle(h, "cache.pool", slog.Int("capacity", 8), slog.Int("running", running), slog.Int("queued", queued))
}

// running plans four builds, finishes one and runs two.
func (s *BandSuite) running(h *PrettyHandler) {
	var steps []PlannedStep
	for _, p := range []string{"a", "b", "c", "d"} {
		steps = append(steps, PlannedStep{Ref: ref(p, "build")})
	}
	s.pool(h, 0, 0)
	s.handle(h, planEvent, slog.Any("steps", steps))
	s.stepEvent(h, "a", "build", StepDone)
	s.stepEvent(h, "b", "build", StepRunning)
	s.stepEvent(h, "c", "build", StepRunning)
	s.pool(h, 2, 1)
}

func (s *BandSuite) failed(h *PrettyHandler) {
	s.fail(h, "api", "build", time.Second)
	s.fail(h, "api", "test", 2500*time.Millisecond)
	s.fail(h, "std", "lint", 3*time.Second)
	s.pool(h, 0, 0)
}

func (s *BandSuite) preview(h *PrettyHandler, n int) {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("line %02d   ", i))
	}
	out[1] = ""
	h.SetSelection(0)
	h.SetPreview(out)
}

// waiting plans docs and test running, with waiters hanging off both.
func (s *BandSuite) waiting(h *PrettyHandler) {
	test, docs := ref("", "test"), ref("docs", "build")
	steps := []PlannedStep{
		{Ref: docs},
		{Ref: test},
		{Ref: ref("console", "build"), Upstreams: []types.TargetRef{docs}},
		{Ref: ref("w0", "lint"), Upstreams: []types.TargetRef{test}},
		{Ref: ref("w1", "lint"), Upstreams: []types.TargetRef{test}},
	}
	s.handle(h, planEvent, slog.Any("steps", steps))
	s.stepEvent(h, "docs", "build", StepRunning)
	s.stepEvent(h, "", "test", StepRunning)
	s.now = s.now.Add(8 * time.Minute)
	s.pool(h, 2, 0)
}

// bandStart is the first row of the handler's band.
func bandStart(h *PrettyHandler) int { return bandScreenRows - h.lease.Rows() + 1 }

func (s *BandSuite) bandTrailing(sc *screen.Screen, h *PrettyHandler) []int {
	var rows []int
	for _, r := range sc.TrailingSpaceRows() {
		if r >= bandStart(h) {
			rows = append(rows, r)
		}
	}
	return rows
}

func (s *BandSuite) noTrailingSpace(width int, draw func(*PrettyHandler)) {
	sc, h := s.handler(width)
	draw(h)
	s.Require().True(h.lease.Enabled())
	s.Empty(s.bandTrailing(sc, h), "a band row ends in padding at %d columns", width)
	for r := bandStart(h); r <= bandScreenRows; r++ {
		s.LessOrEqual(utf8.RuneCountInString(sc.Row(r)), width-1, "row %d reaches the last column at %d columns", r, width)
	}
}

func (s *BandSuite) TestRunningBandWritesNoTrailingSpace() {
	s.noTrailingSpace(80, s.running)
	s.noTrailingSpace(120, s.running)
	s.noTrailingSpace(200, s.running)
}

func (s *BandSuite) TestWaitTreeWritesNoTrailingSpace() {
	s.noTrailingSpace(80, s.waiting)
	s.noTrailingSpace(120, s.waiting)
	s.noTrailingSpace(200, s.waiting)
}

func (s *BandSuite) TestFailureTreeWritesNoTrailingSpace() {
	s.noTrailingSpace(80, s.failed)
	s.noTrailingSpace(120, s.failed)
	s.noTrailingSpace(200, s.failed)
}

func (s *BandSuite) TestPreviewWithTheTreeInFocusWritesNoTrailingSpace() {
	draw := func(h *PrettyHandler) { s.failed(h); s.preview(h, 20) }
	s.noTrailingSpace(80, draw)
	s.noTrailingSpace(120, draw)
	s.noTrailingSpace(200, draw)
}

func (s *BandSuite) TestPreviewWithTheOutputInFocusWritesNoTrailingSpace() {
	draw := func(h *PrettyHandler) { s.failed(h); s.preview(h, 20); h.SetFocus(FocusPreview) }
	s.noTrailingSpace(80, draw)
	s.noTrailingSpace(120, draw)
	s.noTrailingSpace(200, draw)
}

func (s *BandSuite) TestReleaseLeavesTheZoneEmpty() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 20)
	first := bandStart(h)
	s.Require().NotEmpty(sc.Row(first))

	s.Require().NoError(h.ReleaseBand())
	for r := first; r <= bandScreenRows; r++ {
		s.Empty(sc.Row(r), "row %d still holds band text", r)
	}
	s.Empty(s.bandTrailing(sc, h))
}

func (s *BandSuite) TestStatusRowSpinsWhileWorkIsInFlight() {
	sc, h := s.handler(100)
	s.running(h)
	row := sc.Row(bandStart(h))
	s.True(strings.HasPrefix(row, "⠋ 1/4 "), row)
	s.Contains(row, "2/8 slots · 1 queued")
}

func (s *BandSuite) TestSpinnerFrameFollowsTheHandlerClock() {
	sc, h := s.handler(100)
	s.pool(h, 2, 0)
	first := sc.Row(bandScreenRows)
	s.True(strings.HasPrefix(first, "⠋ 2/8 slots"), first)

	s.now = s.now.Add(spinnerEvery)
	s.pool(h, 2, 0)
	s.True(strings.HasPrefix(sc.Row(bandScreenRows), "⠙ 2/8 slots"), sc.Row(bandScreenRows))
}

func (s *BandSuite) TestStatusRowShowsACrossOnceAFailureLeavesNothingRunning() {
	sc, h := s.handler(100)
	s.failed(h)
	row := sc.Row(bandStart(h))
	s.True(strings.HasPrefix(row, "✗ 0 ok · 3 failed"), row)
}

func (s *BandSuite) TestStatusRowShowsACheckForACleanRun() {
	sc, h := s.handler(100)
	s.handle(h, "cache.miss", slog.String("project", "api"), slog.String("target", "build"),
		slog.Int64("duration", int64(time.Second)))
	row := sc.Row(bandScreenRows)
	s.True(strings.HasPrefix(row, "✓ 1 ok"), row)
}

func (s *BandSuite) TestStatusRowOmitsClausesThatSayNothing() {
	sc, h := s.handler(100)
	s.pool(h, 0, 0)
	row := sc.Row(bandScreenRows)
	s.NotContains(row, "slots")
	s.NotContains(row, "queued")
	s.NotContains(row, "failed")
}

func (s *BandSuite) TestProgressBarFillsInEighthCells() {
	filled, track := progressBar(1, 40)
	s.Equal("▌", filled)
	s.Equal(strings.Repeat("░", 19), track)

	filled, track = progressBar(10, 10)
	s.Equal(strings.Repeat("█", progressCols), filled)
	s.Empty(track)
}

func (s *BandSuite) TestProgressBarIsAlwaysTwentyCells() {
	for done := range 11 {
		filled, track := progressBar(done, 10)
		s.Equal(progressCols, utf8.RuneCountInString(filled)+utf8.RuneCountInString(track))
	}
}

func (s *BandSuite) TestMetadataAlignsToTheLongestLabel() {
	sc, h := s.handler(120)
	s.fail(h, "api", "build", time.Second)
	s.fail(h, "api", "integration-tests", 2500*time.Millisecond)
	build, tests := sc.Row(sc.FindRow("├ build")), sc.Row(sc.FindRow("╰ integration-tests"))
	s.Equal(metaColumn(build, "1.0s"), metaColumn(tests, "2.5s"))
	s.Equal(len("  ▸ ╰ integration-tests")-4+metaGap, metaColumn(tests, "2.5s"))
}

func (s *BandSuite) TestMetadataColumnIsCappedAtSixtyColumns() {
	sc, h := s.handler(200)
	s.fail(h, "api", strings.Repeat("t", 100), time.Second)
	row := sc.Row(bandScreenRows)
	s.Equal(maxLabelCols+metaGap, metaColumn(row, "1.0s"))
	s.Contains(row, "...")
}

func (s *BandSuite) TestNarrowTerminalClipsTheLabelAndKeepsTheMetadata() {
	sc, h := s.handler(30)
	s.fail(h, "api", "a-very-long-target-name", time.Second)
	row := sc.Row(bandScreenRows)
	s.True(strings.HasSuffix(row, "1.0s"), row)
	s.Contains(row, "...")
	s.LessOrEqual(utf8.RuneCountInString(row), 29)
}

func (s *BandSuite) TestFailureHeadersUseACrossNotABracketGlyph() {
	sc, h := s.handler(100)
	s.failed(h)
	s.NotZero(sc.FindRow("✗ api"))
	row := sc.Row(sc.FindRow("✗ api"))
	s.NotContains(row, "[fail]")
}

func (s *BandSuite) TestWaitBoardCarriesTheSpinnerInPlaceOfTheStateTag() {
	sc, h := s.handler(120)
	s.waiting(h)
	root := sc.Row(sc.FindRow("(root) test"))
	s.Contains(root, "⠋ (root) test")
	s.NotContains(root, "[run]")
	waiter := sc.Row(sc.FindRow("w0 lint"))
	s.NotContains(waiter, "[wait]")
	s.Contains(waiter, "├ w0 lint")
	s.Contains(sc.Row(sc.FindRow("w1 lint")), "╰ w1 lint")
}

func (s *BandSuite) TestWaitBoardAlignsItsMetadata() {
	sc, h := s.handler(120)
	s.waiting(h)
	root := metaColumn(sc.Row(sc.FindRow("(root) test")), "8m0s")
	waiter := metaColumn(sc.Row(sc.FindRow("w0 lint")), "waiting 8m0s")
	s.Equal(root, waiter)
}

func (s *BandSuite) TestPreviewStacksUnderTheTree() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 6)

	lastBranch := sc.FindRow("╰ lint")
	heading := sc.FindRow("output of api build, last ")
	output := sc.FindRow("line 05")
	s.NotZero(lastBranch)
	s.Less(lastBranch, heading, "the heading sits under the whole tree")
	s.Less(heading, output, "and the output under the heading")
	s.True(strings.HasPrefix(sc.Row(output), "    line"), "indented, so it reads as the heading's body")
}

func (s *BandSuite) TestPreviewShowsTheTailOfTheOutput() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 40)

	s.NotZero(sc.FindRow("line 39"), "the last line is what the capture was kept for")
	s.Zero(sc.FindRow("line 00"))
	heading := sc.Row(sc.FindRow("output of api build"))
	s.Contains(heading, fmt.Sprintf("last %d lines", outputRows(sc)))
}

func (s *BandSuite) TestOutputFocusCollapsesTheTreeToTheSelectedFailure() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 6)
	h.SetFocus(FocusPreview)

	s.Zero(sc.FindRow("╰ lint"), "the rest of the tree is gone")
	collapsed := sc.Row(sc.FindRow("api build"))
	s.Contains(collapsed, "▸ api build")
	s.Contains(collapsed, "(1 of 3)")
	s.NotZero(sc.FindRow("line 05"))
}

func (s *BandSuite) TestOutputFocusGivesTheOutputMoreRows() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 40)
	treeFocus := outputRows(sc)

	h.SetFocus(FocusPreview)
	s.Greater(outputRows(sc), treeFocus)
}

func (s *BandSuite) TestClickingTheTreeUnderAPreviewStillResolves() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 6)

	got, ok := h.HitFailure(sc.FindRow("╰ test"))
	s.Require().True(ok)
	s.Equal("test", got.Target)
	_, ok = h.HitFailure(sc.FindRow("output of api build"))
	s.False(ok, "the heading is not a failure")
	_, ok = h.HitFailure(sc.FindRow("line 05"))
	s.False(ok, "nor is the output")
	_, ok = h.HitFailure(bandStart(h))
	s.False(ok, "nor the status row")
}

func (s *BandSuite) TestClickingTheCollapsedRowResolvesTheSelectedFailure() {
	sc, h := s.handler(100)
	s.failed(h)
	s.preview(h, 6)
	h.SetFocus(FocusPreview)

	got, ok := h.HitFailure(sc.FindRow("▸ api build"))
	s.Require().True(ok)
	s.Equal("build", got.Target)
}

func (s *BandSuite) TestHintShowsEveryActionWhenItFits() {
	text := hintText(FailureHint(100))
	s.Equal("↑↓ select · enter rerun · tab focus · y copy · o output · esc done", text)
}

func (s *BandSuite) TestHintDropsWholeHintsFromTheRight() {
	text := hintText(FailureHint(50))
	s.Equal("↑↓ select · enter rerun · tab focus · esc done", text)
	s.LessOrEqual(tty.Cols(text), 50)
}

func (s *BandSuite) TestHintKeepsTheWayOutOnAVeryNarrowTerminal() {
	s.Equal("esc done", hintText(FailureHint(12)))
	s.Equal("esc done", hintText(FailureHint(1)))
}

func (s *BandSuite) TestHintKeysFollowTheirSpans() {
	var keys []string
	for _, sp := range FailureHint(100)[0].Spans {
		if sp.Key != "" {
			keys = append(keys, sp.Key)
		}
	}
	s.Equal([]string{HintKeyRerun, HintKeyFocus, HintKeyCopy, HintKeyOutput, HintKeyDone}, keys)
}

func (s *BandSuite) TestHintPlainIsOneLine() {
	plain := FailureHintPlain()
	s.NotContains(plain, "\n")
	s.Equal(hintText(FailureHint(0)), plain)
}

func (s *BandSuite) TestTickerRunsWhileWorkIsInFlightAndStopsWhenIdle() {
	_, h := s.handler(100)
	h.tickEvery = time.Millisecond
	s.running(h)
	s.True(s.ticking(h), "work is in flight")

	for _, p := range []string{"b", "c", "d"} {
		s.stepEvent(h, p, "build", StepDone)
	}
	s.pool(h, 0, 0)
	s.False(s.ticking(h), "nothing is in flight")
}

func (s *BandSuite) TestTickerNeverStartsForAnIdleBand() {
	_, h := s.handler(100)
	h.tickEvery = time.Millisecond
	s.pool(h, 0, 0)
	s.False(s.ticking(h))
}

func (s *BandSuite) ticking(h *PrettyHandler) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tickStop != nil
}

var cupRow = regexp.MustCompile(`\x1b\[\d+;1H`)

func (s *BandSuite) TestASpinnerFrameRewritesOnlyTheRowsThatChanged() {
	tap := &tapScreen{Screen: screen.New(120, bandScreenRows)}
	h := s.handlerOn(tap, 120)
	s.waiting(h)
	tap.log.Reset()

	s.now = s.now.Add(spinnerEvery)
	s.pool(h, 2, 0)

	out := tap.log.String()
	s.Equal(3, len(cupRow.FindAllString(out, -1)), "the status row and the two roots, no others")
	s.Equal(1, strings.Count(out, "\x1b7"), "in one write")
}

func (s *BandSuite) TestAnUnchangedFrameWritesNothing() {
	tap := &tapScreen{Screen: screen.New(120, bandScreenRows)}
	h := s.handlerOn(tap, 120)
	s.failed(h)
	tap.log.Reset()

	s.pool(h, 0, 0)
	s.Empty(tap.log.String())
}

func hintText(lines []tty.Line) string {
	var b strings.Builder
	for _, sp := range lines[0].Spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// metaColumn is the 0-based column sub starts in row.
func metaColumn(row, sub string) int {
	i := strings.Index(row, sub)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(row[:i])
}

func outputRows(sc *screen.Screen) int {
	n := 0
	for _, r := range sc.Rows() {
		if strings.HasPrefix(r, "    line ") {
			n++
		}
	}
	return n
}
