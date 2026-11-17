package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// The macOS profile is asserted as text, so it is pinned on every host.
type SeatbeltSuite struct{ suite.Suite }

func TestSeatbeltSuite(t *testing.T) { suite.Run(t, new(SeatbeltSuite)) }

func (s *SeatbeltSuite) TestTheProfileDeniesByDefault() {
	profile := seatbeltProfile(nil)
	s.Equal(seatbeltBase, profile, "no rule grants no path")
	s.True(strings.HasPrefix(profile, "(version 1)\n(deny default)\n"))
	s.NotContains(profile, "(allow default)")
	s.NotContains(profile, "(allow file-read*)\n", "no read is granted everywhere")
	s.NotContains(profile, "(allow process-exec)\n", "no exec is granted everywhere")
	s.Contains(profile, "(allow network*)\n", "the network is open, as on Linux")
	s.Contains(profile, "(allow file-read-metadata)\n", "stat is open, as landlock leaves it")
}

func (s *SeatbeltSuite) TestEachRightIsItsOwnClause() {
	profile := seatbeltProfile([]filesystem.Rule{
		{Path: "/ws", Read: true, Write: true, Exec: true},
		{Path: "/usr/bin", Read: true, Exec: true},
		{Path: "/etc/hosts", Read: true},
		{Path: "/opt/tool", Exec: true},
	})
	s.Equal(seatbeltBase+
		"(allow file-read* (subpath \"/ws\"))\n"+
		"(allow file-write* file-ioctl (subpath \"/ws\"))\n"+
		"(allow process-exec (subpath \"/ws\"))\n"+
		"(allow file-read* (subpath \"/usr/bin\"))\n"+
		"(allow process-exec (subpath \"/usr/bin\"))\n"+
		"(allow file-read* (subpath \"/etc/hosts\"))\n"+
		"(allow process-exec (subpath \"/opt/tool\"))\n",
		profile)
}

func (s *SeatbeltSuite) TestAPathCannotBreakOutOfItsString() {
	profile := seatbeltProfile([]filesystem.Rule{{Path: `/ws/a") (allow default) ("\b`, Read: true}})
	s.Equal(seatbeltBase+`(allow file-read* (subpath "/ws/a\") (allow default) (\"\\b"))`+"\n", profile)
}
