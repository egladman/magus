package progress

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/termkit"
)

type LoaderSuite struct{ suite.Suite }

func TestLoader(t *testing.T) { suite.Run(t, new(LoaderSuite)) }

func text(line termkit.Line) string {
	var b strings.Builder
	for _, s := range line.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func (s *LoaderSuite) mustBuild(l Loader, err error) Loader {
	s.Require().NoError(err)
	return l
}

func (s *LoaderSuite) TestCatalogIsEliPicks() {
	var names []LoaderName
	for _, l := range Catalog() {
		names = append(names, l.Name)
	}
	s.Equal([]LoaderName{
		"rotation/comet·1", "rotation/square·1", "rotation/clock·1",
		"rotation/ring·2", "rotation/pinwheel·2", "rotation/infinity·2",
		"glyph/dots", "glyph/orbit", "glyph/pulse", "glyph/triangle", "glyph/spark", "glyph/line",
		"strip/scanner", "strip/equalizer", "strip/shade", "strip/snake", "strip/sine", "strip/dots",
		"text/shimmer", "text/typewriter", "span/comet", "span/progress", "column/fill", "column/elevator",
	}, names)
}

func (s *LoaderSuite) TestNewGlyphRejectsAnUnknownName() {
	_, err := NewGlyph(GlyphName("glyph/nope"))
	s.Require().Error(err)
	s.Contains(err.Error(), `"glyph/nope"`)
	s.Contains(err.Error(), "glyph/dots")
}

func (s *LoaderSuite) TestNoFrameEndsInASpaceExceptSpans() {
	for _, entry := range Catalog() {
		name := entry.Name
		l := entry.Width(20).Height(8).Label("building internal/cache")
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

func (s *LoaderSuite) TestFixedLoadersKeepTheirFootprint() {
	for _, l := range Catalog() {
		name := l.Name
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

func (s *LoaderSuite) TestGlyphDotsCyclesInkFrames() {
	l := s.mustBuild(NewGlyph(GlyphDots))
	s.Equal(80*time.Millisecond, l.Interval)
	s.Equal("⠋", text(l.Frame(0)[0]))
	s.Equal("⠙", text(l.Frame(80 * time.Millisecond)[0]))
	s.Equal("⠏", text(l.Frame(9 * 80 * time.Millisecond)[0]))
	s.Equal("⠋", text(l.Frame(10 * 80 * time.Millisecond)[0]))
}

func (s *LoaderSuite) TestFrameIsPure() {
	l := s.mustBuild(NewStrip(StripSine))
	s.Equal(l.Frame(777*time.Millisecond), l.Frame(777*time.Millisecond))
}

func (s *LoaderSuite) TestNegativeElapsedDrawsTheFirstFrame() {
	l := s.mustBuild(NewGlyph(GlyphLine))
	s.Equal("|", text(l.Frame(-time.Second)[0]))
}

func (s *LoaderSuite) TestCometCellTrailsTwoDots() {
	l := s.mustBuild(NewRotation(RotationComet))
	// Frame 2 lights rim dots 2, 1 and 0: (1,1), (1,0) and (0,0).
	s.Equal(string(rune(0x2800+0x10+0x08+0x01)), text(l.Frame(2 * l.Interval)[0]))
}

func (s *LoaderSuite) TestScannerHeadStartsAtTheLeft() {
	l := s.mustBuild(NewStrip(StripScanner))
	s.Equal("●·······", text(l.Frame(0)[0]))
}

func (s *LoaderSuite) TestStripDotsDropsItsPadding() {
	l := s.mustBuild(NewStrip(StripDots))
	s.Equal("···", text(l.Frame(0)[0]))
}

func (s *LoaderSuite) TestSpanFillsTheGivenWidth() {
	l := s.mustBuild(NewSpan(SpanComet)).Width(30)
	s.Equal(30, utf8.RuneCountInString(text(l.Frame(time.Second)[0])))
}

func (s *LoaderSuite) TestSpanProgressFillsInEighths() {
	l := s.mustBuild(NewSpan(SpanProgress)).Width(10)
	// Frame 75 is a quarter of the way: two full cells and four eighths.
	s.Equal("██▌░░░░░░░", text(l.Frame(75 * l.Interval)[0]))
}

func (s *LoaderSuite) TestColumnFillsTheGivenHeight() {
	l := s.mustBuild(NewColumn(ColumnElevator)).Height(5)
	lines := l.Frame(0)
	s.Require().Len(lines, 5)
	s.Equal("█", text(lines[0]))
	s.Equal("▒", text(lines[1]))
	s.Equal("│", text(lines[4]))
}

func (s *LoaderSuite) TestWidthLeavesAFixedLoaderAlone() {
	l := s.mustBuild(NewGlyph(GlyphDots)).Width(40)
	s.Equal(1, l.Cols)
}

func (s *LoaderSuite) TestTypewriterTypesTheLabel() {
	l := s.mustBuild(NewText(TextTypewriter)).Label("abc")
	s.Equal("ab", text(l.Frame(2 * l.Interval)[0]))
}

func (s *LoaderSuite) TestFallbackIsASCII() {
	l := s.mustBuild(NewGlyph(GlyphLine))
	for f := range 4 {
		r := text(l.Frame(time.Duration(f) * l.Interval)[0])
		s.Less(r[0], byte(0x80), r)
	}
}
