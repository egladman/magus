package marker

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type ParseSuite struct{ suite.Suite }

func TestParseSuite(t *testing.T) { suite.Run(t, new(ParseSuite)) }

func (s *ParseSuite) TestUntilEndsAtTheClosingParen() {
	s.Equal("no store still serves ed25519 envelopes",
		until("compat(until: no store still serves ed25519 envelopes): sigAlg is"))
}

func (s *ParseSuite) TestUntilTruncatesAConditionThatWraps() {
	s.Equal("no install still carries the pre-rename file - the",
		until("compat(until: no install still carries the pre-rename file - the"))
}

func (s *ParseSuite) TestUntilIsEmptyWithoutACondition() {
	s.Empty(until("compat(until: )"))
}

func (s *ParseSuite) TestParseReadsACompatMarker() {
	f, ok := Parse("\t// compat(until: console reads the new keys): keep it")
	s.True(ok)
	s.Equal(Found{Marker: "compat(until:", Text: "console reads the new keys"}, f)
}

func (s *ParseSuite) TestParseReadsADeprecation() {
	f, ok := Parse("// Deprecated: use G.")
	s.True(ok)
	s.Equal(Found{Marker: "Deprecated:", Text: "use G."}, f)
}

// The scanner once reported its own constant, and its fixtures, as decisions
// governing the reader's change.
func (s *ParseSuite) TestParseIgnoresAStringLiteral() {
	_, ok := Parse(`const Compat = "compat(until: x)"`)
	s.False(ok)
}

func (s *ParseSuite) TestInCommentReadsEachCommentForm() {
	s.True(inComment("\t// "), "a line comment")
	s.True(inComment("  * "), "a block comment continuation")
	s.True(inComment("# "), "a shell or yaml comment")
	s.True(inComment("/* "), "a block comment")
}

func (s *ParseSuite) TestDevOnlyReadsTheNextRelease() {
	s.True(DevOnly("// " + Compat + NextRelease + "): the old reader"))
}

func (s *ParseSuite) TestDevOnlyIgnoresOtherConditions() {
	s.False(DevOnly("// " + Compat + "no store holds v1 rows): the old reader"))
	s.False(DevOnly("// " + Compat + NextRelease + " ships, or later)"))
}

func (s *ParseSuite) TestNearDevOnlyReadsADifferentCase() {
	s.True(NearDevOnly("// " + Compat + "The Next Release): the old reader"))
}

func (s *ParseSuite) TestNearDevOnlyReadsATrailingPeriod() {
	s.True(NearDevOnly("// " + Compat + NextRelease + ".): the old reader"))
}

func (s *ParseSuite) TestNearDevOnlyReadsExtraSpacing() {
	s.True(NearDevOnly("// " + Compat + "the  next release ): the old reader"))
}

func (s *ParseSuite) TestNearDevOnlyIgnoresTheExactCondition() {
	s.False(NearDevOnly("// " + Compat + NextRelease + "): the old reader"))
}

func (s *ParseSuite) TestNearDevOnlyIgnoresOtherConditions() {
	s.False(NearDevOnly("// " + Compat + "the next major release): the old reader"))
}

func (s *ParseSuite) TestDevOnlyIgnoresAMention() {
	s.False(DevOnly("a shim takes `" + Compat + NextRelease + ")`"))
}
