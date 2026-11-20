package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// linkedTree plants links into denied/sub: a/l absolute, a/r relative,
// a/c a link to a/l. Each `<link>/../x.go` lands in denied/.
func (s *BypassClassSuite) linkedTree() []string {
	sub := filepath.Join(s.root, "denied", "sub")
	s.Require().NoError(os.MkdirAll(sub, 0o755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "a"), 0o755))
	s.Require().NoError(os.Symlink(sub, filepath.Join(s.root, "a", "l")))
	s.Require().NoError(os.Symlink(filepath.Join("..", "denied", "sub"), filepath.Join(s.root, "a", "r")))
	s.Require().NoError(os.Symlink("l", filepath.Join(s.root, "a", "c")))
	return []string{"a/l", "a/r", "a/c"}
}

// linkSpellings are the ways to spell `<link>/../x.go`. They are
// built by hand: filepath.Join would clean away the `..` under test.
func linkSpellings(link string) []string {
	return []string{
		link + "/../x.go", fmt.Sprintf("./%s/../x.go", link), strings.Replace(link, "/", "/./", 1) + "/../x.go",
		strings.Replace(link, "/", "//", 1) + "/../x.go", link + "/./../x.go", link + "/../../denied/x.go",
	}
}

// TestPathJudgingMatchesTheKernel writes through every spelling and
// requires the guard to name the file the kernel wrote.
func (s *BypassClassSuite) TestPathJudgingMatchesTheKernel() {
	for _, link := range s.linkedTree() {
		for _, spelling := range linkSpellings(link) {
			abs := fmt.Sprintf("%s/%s", s.root, spelling)
			for _, p := range []string{spelling, abs} {
				s.Require().NoError(os.WriteFile(abs, []byte("x"), 0o644))
				written, err := os.Stat(abs)
				s.Require().NoError(err)
				rel, inside := workspaceRelative(s.root, p)
				s.Require().True(inside, p)
				judged, err := os.Stat(filepath.Join(s.root, rel))
				s.Require().NoError(err, "%q judged as %q, which does not exist", p, rel)
				s.True(os.SameFile(written, judged), "%q judged as %q", p, rel)
				s.True(strings.HasPrefix(rel, "denied/"), "%q judged as %q", p, rel)

				placed, ok := placePath(spelling, s.root, "")
				s.Require().True(ok)
				placedInfo, err := os.Stat(placed)
				s.Require().NoError(err)
				s.True(os.SameFile(written, placedInfo), "%q placed at %q", spelling, placed)
			}
		}
	}
}

// TestASymlinkBeforeADotDotCannotReachADeniedPath runs the path class
// through Judge. A write through a link into a deny path is refused;
// the same write with no link stays inside the lease.
func (s *BypassClassSuite) TestASymlinkBeforeADotDotCannotReachADeniedPath() {
	ctx, root := fleetFixture(s.T(), fixture.FleetLeases()...)
	gen := filepath.Join(root, "cmd", "magus", "gen", "sub")
	s.Require().NoError(os.MkdirAll(gen, 0o755))
	s.Require().NoError(os.Symlink(gen, filepath.Join(root, "cmd", "magus", "l")))

	judge := func(req Request) verdict.Judgment {
		req.Lease, req.Dir, req.DryRun = "lease-b", root, true
		return Judge(ctx, Dependencies{}, req)
	}
	through := filepath.Join(root, "cmd", "magus") + "/l/../cli_flags.go"
	for _, req := range []Request{{Input: through, IsPath: true}, {Input: "echo x > cmd/magus/l/../cli_flags.go"}} {
		got := judge(req)
		s.Contains(got.Reason, "cmd/magus/gen/**", "%q is refused by the deny path it reaches", req.Input)
		want := got
		want.Decision, want.Rule = verdict.Deny, string(denyRuleLeaseWrite)
		s.Equal(want, got, "%q lands in cmd/magus/gen", req.Input)
	}

	direct := filepath.Join(root, "cmd", "magus", "cli_flags.go")
	s.NotEqual(verdict.Deny, judge(Request{Input: direct, IsPath: true}).Decision, "the lease writes cmd/magus itself")
	s.NotEqual(verdict.Deny, judge(Request{Input: "echo x > cmd/magus/cli_flags.go"}).Decision)
}
