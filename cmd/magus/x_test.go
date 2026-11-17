package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// elide keeps MGS8004's columns aligned. Trimming from the LEFT is the point: a
// describe-style version differs in its tail, so a right-trim would render two
// different versions as the same string.
func TestElideKeepsTheDistinguishingTail(t *testing.T) {
	assert.Equal(t, "short", elide("short"))

	a := elide("backup/pre-frosty-improve-terminal-6-g594d75a08-dirty")
	b := elide("backup/pre-frosty-improve-terminal-8-g63eea2d31-dirty")
	assert.NotEqual(t, a, b, "two versions differing only in their tail must not elide to one string")
	assert.LessOrEqual(t, len(a), 22, "the value must fit the aligned column")
	assert.Contains(t, a, "g594d75a08", "the distinguishing part survives")
}

func TestShortRev(t *testing.T) {
	assert.Equal(t, "594d75a08a35", shortRev("594d75a08a358dfc080de4afbee71872f50a7390"))
	assert.Equal(t, "abc", shortRev("abc"), "a value already short is passed through")
	assert.Empty(t, shortRev(""), "an unrecorded revision stays empty rather than becoming a rendered blank")
}

func TestExecCISuite(t *testing.T) { suite.Run(t, new(ExecCISuite)) }

// ExecCISuite pins `magus x ci`: it verifies only, and says so when rw was
// carried.
type ExecCISuite struct{ suite.Suite }

func (s *ExecCISuite) TestExecCIVerifiesOnly() {
	charms, note := execCICharms([]string{types.CharmReadWrite, types.CharmUpdate, "quiet"})

	s.Equal([]string{"quiet"}, charms)
	s.Contains(note, "magus x verifies only")
}

func (s *ExecCISuite) TestExecCIWithoutReadWriteSaysNothing() {
	charms, note := execCICharms([]string{"quiet"})

	s.Equal([]string{"quiet"}, charms)
	s.Empty(note)
}
