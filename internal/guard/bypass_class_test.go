package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/stretchr/testify/suite"
)

// BypassClassSuite proves each bypass class closed over every spelling
// of a denied line, not one example. Each class keeps its allowed
// sibling. The seeds are the probes that found the bypasses 2026-11-18.
type BypassClassSuite struct {
	suite.Suite
	root string
}

func TestBypassClassSuite(t *testing.T) {
	suite.Run(t, new(BypassClassSuite))
}

func (s *BypassClassSuite) SetupTest() {
	s.root = canonicalPath(s.T().TempDir())
}

// deps is the strict rule set in a workspace at s.root, called there.
func (s *BypassClassSuite) deps() Dependencies {
	deps := strict(testDependencies())
	deps.scope = workspaceScope{root: s.root, dir: s.root, home: canonicalPath(s.T().TempDir())}
	return deps
}

func (s *BypassClassSuite) denies(command string) {
	s.T().Helper()
	v := Evaluate(s.T().Context(), s.deps(), command)
	s.NotEmpty(v.Deny, "%q must deny", command)
}

func (s *BypassClassSuite) passes(command string) {
	s.T().Helper()
	v := Evaluate(s.T().Context(), s.deps(), command)
	s.Empty(v.Deny, "%q must not deny (rule %s)", command, v.Rule.Name)
}

// destructiveSeeds are lines the VCS and rewrite rules deny on their own.
var destructiveSeeds = []string{
	"git stash", "git reset --hard", "git clean -fd", "git checkout -- .", "git restore .",
	"git add -A", "hg purge", "jj abandon", "sed -i s/a/b/ f.txt",
}

// unknownLaunchers run their trailing argv and are on no guard list.
// The last is invented: the class is closed only if tomorrow's
// launcher is judged by what it launches.
var unknownLaunchers = []string{
	"watch -n1", "chronic", "flock /tmp/l", "unbuffer", "caffeinate -i", "ionice -c3",
	"taskset 1", "retry --", "ssh host", "zzlaunch-7f3a --flag value",
}

// knownWrappers are peeled already. A stack of both kinds must still
// reach the payload.
var knownWrappers = []string{"", "nohup ", "env A=1 ", "timeout 5 ", "nice -n 5 "}

func (s *BypassClassSuite) TestAnUnknownLauncherIsJudgedByWhatItLaunches() {
	for _, seed := range destructiveSeeds {
		s.denies(seed)
		for _, launcher := range unknownLaunchers {
			for _, outer := range knownWrappers {
				for _, inner := range knownWrappers {
					s.denies(fmt.Sprintf("%s%s %s%s", outer, launcher, inner, seed))
				}
			}
		}
	}
}

func (s *BypassClassSuite) TestALauncherOfHarmlessWorkAndProseNamingAGuardedProgramPass() {
	for _, launcher := range unknownLaunchers {
		s.passes(launcher + " ls -la")
		s.passes(launcher + " git status")
	}
	for _, data := range []string{
		"echo git stash", "printf '%s\\n' git reset --hard", "grep -rn git stash.txt",
		"man git-stash", "which git", "cat git stash",
	} {
		s.passes(data)
	}
}

// wholeTreeSpellings name the whole tree, or cannot be read as
// narrower and so count as it.
func (s *BypassClassSuite) wholeTreeSpellings() []string {
	return []string{
		".", "./", "./.", "a/..", "./a/../.", "..", "../..", "*", "**", "*.go", "?*", "[a-z]*",
		":/", "':(top)'", "':(exclude)vendor'", ":!vendor", "''", s.root, s.root + "/", s.root + "/a/..", "/",
		filepath.Dir(s.root),
	}
}

// narrowSpellings name less than the tree.
var narrowSpellings = []string{"docs/x.md", "internal/guard", "a/../b", "internal/*.go", "./docs"}

// pathSetSites are the rules whose exemption or deny reads a path set.
var pathSetSites = []string{
	"git stash push -- %s", "git stash push -m wip -- %s", "git checkout -- %s",
	"git checkout HEAD -- %s", "git restore %s", "git restore --source=HEAD -- %s",
	"git add %s", "git add -- %s", "jj restore %s", "hg revert %s",
}

func (s *BypassClassSuite) TestAPathSetIsWholeTreeUnlessProvenNarrower() {
	for _, site := range pathSetSites {
		for _, spelling := range s.wholeTreeSpellings() {
			s.denies(fmt.Sprintf(site, spelling))
			s.denies(fmt.Sprintf(site, "docs/x.md "+spelling))
		}
		for _, spelling := range narrowSpellings {
			s.passes(fmt.Sprintf(site, spelling))
		}
	}
}

func (s *BypassClassSuite) TestAPathOutsideTheTreeIsNarrower() {
	outside := filepath.Join(filepath.Dir(s.root), "elsewhere", "x.go")
	s.passes("git restore " + outside)
	s.passes("git restore ../elsewhere/x.go")
	// After a cd the directory is unknown, so this is not provably
	// outside.
	s.denies("cd sub && git restore ../elsewhere/..")
}

// inPlaceSpellings are sed argvs that edit in place. They cover each
// cluster with and without a suffix, each long prefix and BSD's -I.
func inPlaceSpellings() []string {
	var out []string
	for _, cluster := range []string{"i", "ni", "in", "Ei", "nEi", "si", "zi", "ui", "I"} {
		for _, suffix := range []string{"", ".bak", "bak", "~"} {
			if strings.HasSuffix(cluster, "i") || cluster == "I" {
				out = append(out, "-"+cluster+suffix)
			}
		}
		out = append(out, "-"+cluster)
	}
	out = append(out, "-i ''", "-n -i", "-E -i.bak")
	long := "in-place"
	for n := 1; n <= len(long); n++ {
		out = append(out, "--"+long[:n], "--"+long[:n]+"=.orig")
	}
	return out
}

func (s *BypassClassSuite) TestEveryInPlaceSedSpellingDenies() {
	for _, flags := range inPlaceSpellings() {
		for _, files := range []string{"f.txt", "one.go two.go", "**/*.go"} {
			s.denies(fmt.Sprintf("sed %s s/a/b/ %s", flags, files))
			s.denies(fmt.Sprintf("sed %s -e s/a/b/ %s", flags, files))
		}
	}
}

func (s *BypassClassSuite) TestSedThatPrintsPasses() {
	for _, line := range []string{
		"sed -n 1p f.txt", "sed s/a/b/ f.txt", "sed -E s/a/b/ f.txt", "cat f | sed s/x/y/",
		"sed -ei f.txt", "sed --expression=s/in-place/x/ f.txt", "sed -f in-place.sed f.txt",
		"sed --expression i f.txt",
	} {
		s.passes(line)
	}
}

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

// linkSpellings are the ways to spell `<link>/../x.go`.
func linkSpellings(link string) []string {
	return []string{
		link + "/../x.go", "./" + link + "/../x.go", strings.Replace(link, "/", "/./", 1) + "/../x.go",
		strings.Replace(link, "/", "//", 1) + "/../x.go", link + "/./../x.go", link + "/../../denied/x.go",
	}
}

// TestPathJudgingMatchesTheKernel writes through every spelling and
// requires the guard to name the file the kernel wrote.
func (s *BypassClassSuite) TestPathJudgingMatchesTheKernel() {
	for _, link := range s.linkedTree() {
		for _, spelling := range linkSpellings(link) {
			for _, p := range []string{spelling, s.root + "/" + spelling} {
				s.Require().NoError(os.WriteFile(s.root+"/"+spelling, []byte("x"), 0o644))
				written, err := os.Stat(s.root + "/" + spelling)
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
		v := judge(req)
		s.Equal(verdict.Deny, v.Decision, "%q lands in cmd/magus/gen", req.Input)
		s.Equal(string(denyRuleLeaseWrite), v.Rule, "%q", req.Input)
		s.Contains(v.Reason, "cmd/magus/gen/**", "%q is refused by the deny path it reaches", req.Input)
	}

	direct := filepath.Join(root, "cmd", "magus", "cli_flags.go")
	s.NotEqual(verdict.Deny, judge(Request{Input: direct, IsPath: true}).Decision, "the lease writes cmd/magus itself")
	s.NotEqual(verdict.Deny, judge(Request{Input: "echo x > cmd/magus/cli_flags.go"}).Decision)
}

// prefixes are every abbreviation parse-options accepts for --long.
func prefixes(long string) []string {
	var out []string
	for n := 1; n <= len(long); n++ {
		out = append(out, "--"+long[:n])
	}
	return out
}

// discardingOptions are argvs whose option, not their path set,
// discards uncommitted work or stages all of it. %s is the option.
var discardingOptions = map[string][]string{
	"git reset %s":                        prefixes("hard"),
	"git reset %s HEAD~1":                 prefixes("hard"),
	"git checkout %s main":                append(prefixes("force"), "-f", "-qf"),
	"git checkout -B main origin/main %s": append(prefixes("force"), "-f"),
	"git switch %s main":                  append(append(prefixes("discard-changes"), prefixes("force")...), "-f"),
	"git read-tree -u %s HEAD":            prefixes("reset"),
	"git commit %s -m wip":                append(prefixes("all"), "-a", "-va"),
	"git commit %s wip":                   {"-am", "-vam"},
	"git add %s":                          append(append(prefixes("all"), prefixes("update")...), "-A", "-Av", "-vu"),
	"git clean -d %s":                     prefixes("force"),
	"git commit %s -m wip -- docs/x.md":   prefixes("no-verify")[3:],
}

func (s *BypassClassSuite) TestADiscardingOptionDeniesUnderEverySpelling() {
	for site, spellings := range discardingOptions {
		for _, spelling := range spellings {
			s.denies(fmt.Sprintf(site, spelling))
		}
	}
	for _, line := range []string{"git commit .", "git commit -m wip -- .", "git commit -m wip :/"} {
		s.denies(line)
	}
}

func (s *BypassClassSuite) TestTheOptionsBesideADiscardingOnePass() {
	for _, line := range []string{
		"git reset --soft HEAD~1", "git reset HEAD -- docs/x.md", "git checkout main",
		"git checkout -b f", "git checkout -B f origin/main", "git switch main", "git switch -c f",
		"git switch --detach main", "git switch -cf", "git read-tree HEAD", "git read-tree -m -u HEAD",
		"git commit -m wip", "git commit -ma", "git commit -m 'fix: x .'", "git commit --amend --no-edit",
		"git commit --allow-empty -m wip", "git commit -F -", "git commit --author 'A <a@b>' -m wip",
		"git commit -m wip -- docs/x.md", "git add -p docs/x.md", "git add --chmod=+x docs/x.sh",
		"git clean -n", "git clean --dry-run -d",
	} {
		s.passes(line)
	}
}
