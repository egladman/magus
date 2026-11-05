package marker

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type MarkerSuite struct{ suite.Suite }

func TestMarkerSuite(t *testing.T) { suite.Run(t, new(MarkerSuite)) }

func (s *MarkerSuite) TestUntilEndsAtTheClosingParen() {
	s.Equal("no store still serves ed25519 envelopes",
		until("compat(until: no store still serves ed25519 envelopes): sigAlg is"))
}

func (s *MarkerSuite) TestUntilTruncatesAConditionThatWraps() {
	s.Equal("no install still carries the pre-rename file - the",
		until("compat(until: no install still carries the pre-rename file - the"))
}

func (s *MarkerSuite) TestUntilIsEmptyWithoutACondition() {
	s.Empty(until("compat(until: )"))
}

func (s *MarkerSuite) TestParseReadsACompatMarker() {
	f, ok := Parse("\t// compat(until: console reads the new keys): keep it")
	s.True(ok)
	s.Equal(Found{Marker: "compat(until:", Text: "console reads the new keys"}, f)
}

func (s *MarkerSuite) TestParseReadsADeprecation() {
	f, ok := Parse("// Deprecated: use G.")
	s.True(ok)
	s.Equal(Found{Marker: "Deprecated:", Text: "use G."}, f)
}

// The scanner once reported its own constant, and its fixtures, as decisions
// governing the reader's change.
func (s *MarkerSuite) TestParseIgnoresAStringLiteral() {
	_, ok := Parse(`const Compat = "compat(until: x)"`)
	s.False(ok)
}

func (s *MarkerSuite) TestInCommentReadsEachCommentForm() {
	s.True(inComment("\t// "), "a line comment")
	s.True(inComment("  * "), "a block comment continuation")
	s.True(inComment("# "), "a shell or yaml comment")
	s.True(inComment("/* "), "a block comment")
}

func (s *MarkerSuite) TestDevOnlyReadsTheNextRelease() {
	s.True(DevOnly("// " + Compat + NextRelease + "): the old reader"))
}

func (s *MarkerSuite) TestDevOnlyIgnoresOtherConditions() {
	s.False(DevOnly("// " + Compat + "no store holds v1 rows): the old reader"))
	s.False(DevOnly("// " + Compat + NextRelease + " ships, or later)"))
}

func (s *MarkerSuite) TestDevOnlyIgnoresAMention() {
	s.False(DevOnly("a shim takes `" + Compat + NextRelease + ")`"))
}
