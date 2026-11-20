package guard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type ShellPayloadLoopSuite struct {
	suite.Suite
}

func TestShellPayloadLoopSuite(t *testing.T) { suite.Run(t, new(ShellPayloadLoopSuite)) }

func (s *ShellPayloadLoopSuite) TestShWhileLoopIsABusyWait() {
	s.True(busyWaitFires(`sh -c 'while ! test -f done; do sleep 5; done'`, DialectBash))
}

func (s *ShellPayloadLoopSuite) TestNohupTickLoopIsABusyWait() {
	s.True(busyWaitFires(`nohup sh -c 'while true; do sleep 540; echo tick; done' &`, DialectBash))
}

func (s *ShellPayloadLoopSuite) TestBashUntilLoopIsABusyWait() {
	s.True(busyWaitFires(`bash -c 'until test -f done; do sleep 5; done'`, DialectBash))
}

func (s *ShellPayloadLoopSuite) TestQuotedLoopInAnEchoIsNot() {
	s.False(busyWaitFires(`echo 'until grep -q x f; do sleep 1; done'`, DialectBash))
}

func (s *ShellPayloadLoopSuite) TestPayloadLoopProbingAPidIsAForeignWait() {
	s.True(foreignWaitFires(`sh -c 'while kill -0 123; do sleep 5; done'`, DialectBash))
}

func (s *ShellPayloadLoopSuite) TestPayloadLoopMarksItsCommandsRepeating() {
	got := commandInvocations(`bash -c 'until gh pr checks 1; do sleep 30; done'`, DialectBash, "/work")
	s.Equal([]types.CommandInvocation{
		{Program: "gh", Args: []string{"pr", "checks", "1"}, Repeats: true},
		{Program: "sleep", Args: []string{"30"}, Repeats: true},
	}, got)
}

func (s *ShellPayloadLoopSuite) TestPayloadInsideAnOuterLoopRepeats() {
	got := commandInvocations(`while true; do sh -c 'gh run list'; done`, DialectBash, "/work")
	s.Equal([]types.CommandInvocation{
		{Program: "true", Args: []string{}, Repeats: true},
		{Program: "gh", Args: []string{"run", "list"}, Repeats: true},
	}, got)
}

// A version-control remove or move is a write of each path it names, and of nothing else
// on its line: a global's value (`git -C sub`) and the subcommand are words a write path
// could happen to name.
func TestVCSRemovesAndMovesOfferTheirPaths(t *testing.T) {
	for line, want := range map[string][]string{
		"git rm -r internal/ledger":            {"internal/ledger"},
		"git -C sub rm --cached a.go":          {"a.go"},
		"git mv cmd/magus/a.go cmd/magus/b.go": {"cmd/magus/a.go", "cmd/magus/b.go"},
		"git rm -- -odd.go":                    {"-odd.go"},
		"hg remove -I pattern a.go":            {"a.go"},
		"sl rename a.go b.go":                  {"a.go", "b.go"},
		"git status":                           nil,
		"git log -- a.go":                      nil,
		"hg status a.go":                       nil,
	} {
		assert.Equal(t, want, writeTargetCandidates(line, DialectBash), line)
	}
	assert.False(t, onlyReads("git", "", []string{"rm", "a.go"}))
	assert.False(t, onlyReads("git", "", []string{"-C", "x", "mv", "a", "b"}))
	assert.True(t, onlyReads("git", "", []string{"log", "--", "rm"}))
}

// lease-write grades a remove or move the way it grades rm and mv: inside the lease's
// write paths it passes, and outside them, a directory included, it is refused naming the
// owner. A line naming no lease is never graded.
func TestLeaseWriteGradesVCSRemovesAndMoves(t *testing.T) {
	ctx, _ := fleetFixture(t, writePathFleet()...)
	for _, line := range []string{
		"git rm cmd/magus/old.go",
		"git mv cmd/magus/a.go cmd/magus/b.go",
		"git rm -r --cached cmd/magus/sub",
		"hg remove cmd/magus/old.go",
	} {
		assert.Empty(t, denyWriteOutsideLease(ctx, Dependencies{}, "lease-b", line), line)
	}
	for _, line := range []string{
		"git rm internal/ledger/store.go",
		"git rm -r internal/ledger",
		"git mv cmd/magus/a.go internal/ledger/a.go",
		"sl rename internal/ledger/x.go cmd/magus/x.go",
	} {
		assert.Contains(t, denyWriteOutsideLease(ctx, Dependencies{}, "lease-b", line), "lease-a", line)
	}
	assert.NotEmpty(t, denyWriteOutsideLease(ctx, Dependencies{}, "lease-b", "git rm cmd/magus/gen/flags.go"), "the lease's own deny paths")
	assert.Empty(t, denyWriteOutsideLease(ctx, Dependencies{}, "", "git rm internal/ledger/store.go"))
}

func TestDescriptorDuplicationNamesNoWriteTarget(t *testing.T) {
	cases := map[string][]string{
		"./magus ls jobs 2>&1":       nil,
		"make 1>&2":                  nil,
		"make 2>&-":                  nil,
		"make >&out.log":             {"out.log"},
		"make > out.log 2>&1":        {"out.log"},
		"sh -c 'make 2>&1 >x.log'":   {"x.log"},
		"make 2>&1 &>> combined.log": {"combined.log"},
	}
	for line, want := range cases {
		assert.Equal(t, want, redirectTargets(line, 0, DialectBash), "redirectTargets(%q)", line)
		got := writeTargetCandidates(line, DialectBash)
		for _, w := range want {
			assert.Contains(t, got, w, "writeTargetCandidates(%q)", line)
		}
		assert.NotContains(t, got, "1", "writeTargetCandidates(%q)", line)
		assert.NotContains(t, got, "2", "writeTargetCandidates(%q)", line)
		assert.NotContains(t, got, "-", "writeTargetCandidates(%q)", line)
	}
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

// inPlaceSpellings are sed argvs that edit in place. They cover each
// cluster with and without a suffix, each long prefix and BSD's -I.
func inPlaceSpellings() []string {
	var out []string
	for _, cluster := range []string{"i", "ni", "in", "Ei", "nEi", "si", "zi", "ui", "I"} {
		for _, suffix := range []string{"", ".bak", "bak", "~"} {
			if strings.HasSuffix(cluster, "i") || cluster == "I" {
				out = append(out, fmt.Sprintf("-%s%s", cluster, suffix))
			}
		}
		out = append(out, "-"+cluster)
	}
	out = append(out, "-i ''", "-n -i", "-E -i.bak")
	for _, p := range prefixes("in-place") {
		out = append(out, p, p+"=.orig")
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
