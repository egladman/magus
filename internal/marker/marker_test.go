package marker_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/marker"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type MarkerSuite struct{ suite.Suite }

func TestMarkerSuite(t *testing.T) { suite.Run(t, new(MarkerSuite)) }

func (s *MarkerSuite) TestUntilEndsAtTheClosingParen() {
	s.Equal("no store still serves ed25519 envelopes",
		marker.Until("compat(until: no store still serves ed25519 envelopes): sigAlg is"))
}

func (s *MarkerSuite) TestUntilTruncatesAConditionThatWraps() {
	s.Equal("no install still carries the pre-rename file - the",
		marker.Until("compat(until: no install still carries the pre-rename file - the"))
}

func (s *MarkerSuite) TestUntilIsEmptyWithoutACondition() {
	s.Empty(marker.Until("compat(until: )"))
}

func (s *MarkerSuite) TestParseReadsACompatMarker() {
	f, ok := marker.Parse("\t// compat(until: console reads the new keys): keep it")
	s.True(ok)
	s.Equal(marker.Found{Marker: "compat(until:", Text: "console reads the new keys"}, f)
}

func (s *MarkerSuite) TestParseReadsADeprecation() {
	f, ok := marker.Parse("// Deprecated: use G.")
	s.True(ok)
	s.Equal(marker.Found{Marker: "Deprecated:", Text: "use G."}, f)
}

// The scanner once reported its own constant, and its fixtures, as decisions
// governing the reader's change.
func (s *MarkerSuite) TestParseIgnoresAStringLiteral() {
	_, ok := marker.Parse(`const Compat = "compat(until: x)"`)
	s.False(ok)
}

func (s *MarkerSuite) TestInCommentReadsEachCommentForm() {
	s.True(marker.InComment("\t// "), "a line comment")
	s.True(marker.InComment("  * "), "a block comment continuation")
	s.True(marker.InComment("# "), "a shell or yaml comment")
	s.True(marker.InComment("/* "), "a block comment")
}

func (s *MarkerSuite) TestDevOnlyReadsTheNextRelease() {
	s.True(marker.DevOnly("// " + marker.Compat + marker.NextRelease + "): the old reader"))
}

func (s *MarkerSuite) TestDevOnlyIgnoresOtherConditions() {
	s.False(marker.DevOnly("// " + marker.Compat + "no store holds v1 rows): the old reader"))
	s.False(marker.DevOnly("// " + marker.Compat + marker.NextRelease + " ships, or later)"))
}

func (s *MarkerSuite) TestDevOnlyIgnoresAMention() {
	s.False(marker.DevOnly("a shim takes `" + marker.Compat + marker.NextRelease + ")`"))
}
