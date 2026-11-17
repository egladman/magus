package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// The macOS profile is asserted as text, so it is pinned on every host.
type SeatbeltSuite struct{ suite.Suite }

func TestSeatbeltSuite(t *testing.T) { suite.Run(t, new(SeatbeltSuite)) }

func (s *SeatbeltSuite) TestTheProfileDeniesByDefault() {
	profile := seatbeltProfile(nil, "")
	s.Equal(seatbeltBase, profile, "no rule grants no path")
	s.True(strings.HasPrefix(profile, "(version 1)\n(deny default)\n"))
	s.NotContains(profile, "(allow default)")
	s.NotContains(profile, "(allow file-read*)\n", "no read is granted everywhere")
	s.NotContains(profile, "(allow process-exec)\n", "no exec is granted everywhere")
	s.Contains(profile, "(allow network*)\n", "the network is open, as on Linux")
	s.Contains(profile, "(allow file-read-metadata)\n", "stat is open, as landlock leaves it")
	s.Contains(profile, `(allow file-read* (subpath "/System"))`, "dyld maps the shared cache from under /System")
	s.Contains(profile, `(allow file-read* (subpath "/private/var/db/dyld"))`)
	s.Contains(profile, `(allow file-read* file-write* (literal "/dev/null"))`)
	s.Contains(profile, `(allow file-read* (literal "/dev/urandom") (literal "/dev/tty"))`)
	s.Contains(profile, `(allow file-read* (subpath "/dev/fd"))`)
}

// An exec of a link whose target an exec rule covers grants the link too: the rule names
// the resolved target, and sandbox-exec is asked to run the link.
func (s *SeatbeltSuite) TestASymlinkedExecTargetIsGrantedWhenItsTargetIs() {
	dir := filesystem.ResolveRulePath(s.T().TempDir())
	bin, links := filepath.Join(dir, "bin"), filepath.Join(dir, "links")
	s.Require().NoError(os.MkdirAll(bin, 0o755))
	s.Require().NoError(os.MkdirAll(links, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(bin, "tool"), nil, 0o755))
	link := filepath.Join(links, "tool")
	s.Require().NoError(os.Symlink(filepath.Join(bin, "tool"), link))

	granted := seatbeltProfile([]filesystem.Rule{{Path: bin, Read: true, Exec: true}}, link)
	s.Contains(granted, "(allow process-exec (literal "+seatbeltString(link)+"))")

	other := filepath.Join(dir, "elsewhere")
	refused := seatbeltProfile([]filesystem.Rule{{Path: other, Read: true, Exec: true}}, link)
	s.NotContains(refused, "(literal "+seatbeltString(link)+")", "a link to a file no exec rule covers grants nothing")

	plain := seatbeltProfile([]filesystem.Rule{{Path: bin, Read: true, Exec: true}}, filepath.Join(bin, "tool"))
	s.Equal(seatbeltProfile([]filesystem.Rule{{Path: bin, Read: true, Exec: true}}, ""), plain, "a target that is no link adds nothing")
}

func (s *SeatbeltSuite) TestEachRightIsItsOwnClause() {
	profile := seatbeltProfile([]filesystem.Rule{
		{Path: "/ws", Read: true, Write: true, Exec: true},
		{Path: "/usr/bin", Read: true, Exec: true},
		{Path: "/etc/hosts", Read: true},
		{Path: "/opt/tool", Exec: true},
	}, "")
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
	profile := seatbeltProfile([]filesystem.Rule{{Path: `/ws/a") (allow default) ("\b`, Read: true}}, "")
	s.Equal(seatbeltBase+`(allow file-read* (subpath "/ws/a\") (allow default) (\"\\b"))`+"\n", profile)
}
