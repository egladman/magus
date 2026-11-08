package progress

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/termkit"
)

type ProgressSuite struct{ suite.Suite }

func TestProgress(t *testing.T) { suite.Run(t, new(ProgressSuite)) }

func text(line termkit.Line) string {
	var b strings.Builder
	for _, s := range line.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func (s *ProgressSuite) mustLookup(name string) Loader {
	l, err := Lookup(name)
	s.Require().NoError(err)
	return l
}

func (s *ProgressSuite) TestCatalogIsEliPicks() {
	s.Equal([]string{
		"rotation/comet·1", "rotation/square·1", "rotation/clock·1",
		"rotation/ring·2", "rotation/pinwheel·2", "rotation/infinity·2",
		"glyph/dots", "glyph/orbit", "glyph/pulse", "glyph/triangle", "glyph/spark", "glyph/line",
		"strip/scanner", "strip/equalizer", "strip/shade", "strip/snake", "strip/sine", "strip/dots",
		"text/shimmer", "text/typewriter", "span/comet", "span/progress", "column/fill", "column/elevator",
	}, Names())
}

func (s *ProgressSuite) TestLookupFailsLoudlyOnAnUnknownName() {
	_, err := Lookup("glyph/nope")
	s.Require().Error(err)
	s.Contains(err.Error(), `"glyph/nope"`)
	s.Contains(err.Error(), "glyph/dots")
}

func (s *ProgressSuite) TestNoFrameEndsInASpaceExceptSpans() {
	for _, name := range Names() {
		l := s.mustLookup(name).Width(20).Height(8).Label("building internal/cache")
		for f := range 400 {
			for _, line := range l.Frame(time.Duration(f) * l.Interval) {
				if l.Kind == Span {
					continue
				}
				s.False(strings.HasSuffix(text(line), " "), "%s frame %d: %q", name, f, text(line))
			}
		}
	}
}

func (s *ProgressSuite) TestFixedLoadersKeepTheirFootprint() {
	for _, name := range Names() {
		l := s.mustLookup(name)
		if l.Kind != Glyph && l.Kind != Rotation && l.Kind != Strip {
			continue
		}
		for f := range 200 {
			lines := l.Frame(time.Duration(f) * l.Interval)
			s.Require().Len(lines, 1, name)
			s.LessOrEqual(utf8.RuneCountInString(text(lines[0])), l.Cols, "%s frame %d", name, f)
		}
	}
}

func (s *ProgressSuite) TestGlyphDotsCyclesInkFrames() {
	l := s.mustLookup("glyph/dots")
	s.Equal(80*time.Millisecond, l.Interval)
	s.Equal("⠋", text(l.Frame(0)[0]))
	s.Equal("⠙", text(l.Frame(80 * time.Millisecond)[0]))
	s.Equal("⠏", text(l.Frame(9 * 80 * time.Millisecond)[0]))
	s.Equal("⠋", text(l.Frame(10 * 80 * time.Millisecond)[0]))
}

func (s *ProgressSuite) TestFrameIsPure() {
	l := s.mustLookup("strip/sine")
	s.Equal(l.Frame(777*time.Millisecond), l.Frame(777*time.Millisecond))
}

func (s *ProgressSuite) TestNegativeElapsedDrawsTheFirstFrame() {
	l := s.mustLookup("glyph/line")
	s.Equal("|", text(l.Frame(-time.Second)[0]))
}

func (s *ProgressSuite) TestCometCellTrailsTwoDots() {
	l := s.mustLookup("rotation/comet·1")
	// Frame 2 lights rim dots 2, 1 and 0: (1,1), (1,0) and (0,0).
	s.Equal(string(rune(0x2800+0x10+0x08+0x01)), text(l.Frame(2 * l.Interval)[0]))
}

func (s *ProgressSuite) TestScannerHeadStartsAtTheLeft() {
	l := s.mustLookup("strip/scanner")
	s.Equal("●·······", text(l.Frame(0)[0]))
}

func (s *ProgressSuite) TestStripDotsDropsItsPadding() {
	l := s.mustLookup("strip/dots")
	s.Equal("···", text(l.Frame(0)[0]))
}

func (s *ProgressSuite) TestSpanFillsTheGivenWidth() {
	l := s.mustLookup("span/comet").Width(30)
	s.Equal(30, utf8.RuneCountInString(text(l.Frame(time.Second)[0])))
}

func (s *ProgressSuite) TestSpanProgressFillsInEighths() {
	l := s.mustLookup("span/progress").Width(10)
	// Frame 75 is a quarter of the way: two full cells and four eighths.
	s.Equal("██▌░░░░░░░", text(l.Frame(75 * l.Interval)[0]))
}

func (s *ProgressSuite) TestColumnFillsTheGivenHeight() {
	l := s.mustLookup("column/elevator").Height(5)
	lines := l.Frame(0)
	s.Require().Len(lines, 5)
	s.Equal("█", text(lines[0]))
	s.Equal("▒", text(lines[1]))
	s.Equal("│", text(lines[4]))
}

func (s *ProgressSuite) TestWidthLeavesAFixedLoaderAlone() {
	l := s.mustLookup("glyph/dots").Width(40)
	s.Equal(1, l.Cols)
}

func (s *ProgressSuite) TestTypewriterTypesTheLabel() {
	l := s.mustLookup("text/typewriter").Label("abc")
	s.Equal("ab", text(l.Frame(2 * l.Interval)[0]))
}

func (s *ProgressSuite) TestFallbackIsASCII() {
	l := s.mustLookup(Fallback)
	for f := range 4 {
		r := text(l.Frame(time.Duration(f) * l.Interval)[0])
		s.Less(r[0], byte(0x80), r)
	}
}

func (s *ProgressSuite) TestBarFillsInEighths() {
	s.Equal("█████░░░░░", text(Bar(1, 2, 10, termkit.SGRDim)))
	s.Equal("▌░", text(Bar(1, 4, 2, termkit.SGRDim)))
}

func (s *ProgressSuite) TestBarClampsDone() {
	s.Equal("████", text(Bar(9, 4, 4, termkit.SGRDim)))
	s.Equal("░░░░", text(Bar(-1, 4, 4, termkit.SGRDim)))
}

func (s *ProgressSuite) TestBarWithNoTotalDrawsNothing() {
	s.Empty(Bar(1, 0, 10, termkit.SGRDim).Spans)
}
