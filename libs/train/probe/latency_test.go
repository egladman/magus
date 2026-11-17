package probe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type LatencySuite struct{ suite.Suite }

func TestLatency(t *testing.T) { suite.Run(t, new(LatencySuite)) }

func (s *LatencySuite) TestTypingOrdersByFileThenPositionAndSkipsEmptyMiddles() {
	cs := []Cursor{
		{Path: "b.buzz", Start: 5, Prefix: "pb5", Middle: "x", Suffix: "S"},
		{Path: "a.buzz", Start: 90, Prefix: "pa90", Middle: "y", Suffix: "S"},
		{Path: "a.buzz", Start: 10, Prefix: "pa10", Middle: "", Suffix: "S"},
		{Path: "a.buzz", Start: 20, Prefix: "pa20", Middle: "z", Suffix: "S"},
	}
	got := Typing(cs, 0)
	s.Require().Len(got, 3)
	s.Equal("pa20", got[0].Primer)
	s.Equal("pa90", got[1].Primer)
	s.Equal("pb5", got[2].Primer)
	s.Equal("pa20z", got[0].Prefix)
}

func (s *LatencySuite) TestTypingKeepsTheSuffixAndTypesAtMostTheBudget() {
	mid := strings.Repeat("a", TypedBytes+100)
	got := Typing([]Cursor{{Path: "a", Prefix: "P", Middle: mid, Suffix: "SUF"}}, 0)
	s.Require().Len(got, 1)
	s.Equal("SUF", got[0].Suffix)
	s.Equal(TypedBytes, got[0].Typed)
	s.Equal("P"+mid[:TypedBytes], got[0].Prefix)
}

func (s *LatencySuite) TestTypingCutsOnACharacterBoundary() {
	mid := strings.Repeat("a", TypedBytes-1) + "é" + "tail"
	got := Typing([]Cursor{{Path: "a", Middle: mid}}, 0)
	s.Require().Len(got, 1)
	s.Equal(TypedBytes-1, got[0].Typed)
}

func (s *LatencySuite) TestTypingStopsAtMax() {
	var cs []Cursor
	for i := range 10 {
		cs = append(cs, Cursor{Path: "a", Start: i, Middle: "m"})
	}
	s.Len(Typing(cs, 4), 4)
}
