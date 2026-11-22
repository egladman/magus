package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/termkit/internal/cells"
	"github.com/egladman/magus/libs/termkit/internal/termtest"
)

type SpinSuite struct{ suite.Suite }

func TestSpin(t *testing.T) { suite.Run(t, new(SpinSuite)) }

func (s *SpinSuite) TestASpinnerPaintsTheLabelAndStopErasesIt() {
	var out lockedTerminal
	sp, err := StartSpinner(&out, termtest.Terminal(80, 24), "surveying the disk")
	s.Require().NoError(err)
	sp.Stop()
	raw := out.String()
	s.Contains(termtest.StripANSI(raw), "surveying the disk", "the first frame is painted before Stop can win")
	s.Contains(raw, termtest.EL2, "the line is erased before Stop returns")
}

func (s *SpinSuite) TestNothingIsWrittenOnceStopReturns() {
	var out lockedTerminal
	clock := time.Unix(0, 0)
	sp, err := startSpinner(&out, termtest.Terminal(80, 24), "x", func() time.Time { return clock })
	s.Require().NoError(err)
	sp.Stop()
	after := out.String()
	time.Sleep(2 * time.Millisecond)
	s.Equal(after, out.String())
}

func (s *SpinSuite) TestASecondStopIsANoOp() {
	var out lockedTerminal
	sp, err := StartSpinner(&out, termtest.Terminal(80, 24), "x")
	s.Require().NoError(err)
	sp.Stop()
	s.NotPanics(sp.Stop)
}

func (s *SpinSuite) TestSpinFramePadsTheLoaderToItsFootprint() {
	l, err := NewGlyph(GlyphDots)
	s.Require().NoError(err)
	frame := spinFrame(l, 0, "building", false)
	s.True(strings.HasSuffix(frame, " building"), frame)
	s.Equal(l.Cols+len(" building"), cells.Cols(frame))
}

func (s *SpinSuite) TestSpinFrameColorsOnlyWhenAsked() {
	l, err := NewGlyph(GlyphDots)
	s.Require().NoError(err)
	s.NotContains(spinFrame(l, 0, "x", false), "\x1b[")
	s.Equal(spinFrame(l, 0, "x", false), termtest.StripANSI(spinFrame(l, 0, "x", true)))
}

// lockedTerminal is a buffer the spinner's goroutine and the test can share,
// with a descriptor so a probe that says "terminal" is believed.
type lockedTerminal struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *lockedTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.Write(p)
}

func (t *lockedTerminal) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

func (*lockedTerminal) Fd() uintptr { return 2 }
