package vcs

import (
	"context"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// toolFloor is the oldest release whose flags this package passes. A minimum
// only: a ceiling would reject a newer binary over a bound nobody verified.
//
// The comparison is the spell one. spells.ExtractVersion pulls the version out
// of the probe's prose, and VersionBounds.Check decides. Output with no
// version in it is not a failure, and a missing binary is not "too old": the
// command that needed it reports that itself.
//
// flag is the one that sets the floor, named with the command it belongs to.
// A global flag has to precede the subcommand; vcsExec places --color=never
// there, and that flag is older than every floor below. A subcommand flag is
// an argument of that subcommand. A binary that reports a new enough version
// and still lacks the flag fails at the call, which is what git merge-tree's
// exit 129 is for.
type toolFloor struct {
	probe []string
	min   string
	flag  string
}

// toolFloors is keyed by the binary name gitExec and vcsExec pass to exec.
//
// git 2.40: merge-tree --merge-base is a flag of merge-tree (Git 2.40).
// --write-tree and --no-messages are flags of that same subcommand.
// --end-of-options is a rev-parse option from 2.29, and cat-file --batch is
// older than both.
//
// jj 0.22: workspace add --sparse-patterns is a flag of workspace add, added in
// 0.22. That release also renamed the log template keyword to bookmarks, which
// is what `jj log -T` is given. file show is a subcommand since 0.19.
//
// hg 4.5: merge --abort is a flag of merge, added in Mercurial 4.5.
// serve --cmdserver is a flag of serve since 1.9. --color=never is global
// since 4.2.
//
// sl 0.2.20230523: status --root-relative is a flag of status (and of
// resolve --list), not a global option. The 2023-05-23 release notes a fix
// for --no-root-relative, so the flag was there by then. The HHMMSS after
// the date is a build clock, dropped before the comparison: semver would
// read it as a prerelease, and a leading zero (a morning build) makes the
// token invalid, which would skip the floor.
var toolFloors = map[string]toolFloor{
	"git": {probe: []string{"--version"}, min: "2.40", flag: "merge-tree --merge-base"},
	"jj":  {probe: []string{"version"}, min: "0.22", flag: "workspace add --sparse-patterns"},
	"hg":  {probe: []string{"version"}, min: "4.5", flag: "merge --abort"},
	"sl":  {probe: []string{"version"}, min: "0.2.20230523", flag: "status --root-relative"},
}

// toolVersionTimeout bounds a probe that never returns. It matches the spell
// probe: long enough for a cold binary, not a latency target.
const toolVersionTimeout = 10 * time.Second

// toolProbe remembers one binary's floor check for this process. A hook is a
// new process, so the memo saves the forks after the first command, not the
// fork the process pays once.
type toolProbe struct {
	mu   sync.Mutex
	done bool
	err  error
}

var toolProbes sync.Map // binary name -> *toolProbe

// noteToolVersion sets cmd.Err when bin is older than its floor, so Start
// returns that error and runs nothing. An error already on cmd wins: a
// tampered checkout must not be reported as a version miss.
func noteToolVersion(ctx context.Context, cmd *exec.Cmd, bin string) {
	if cmd.Err != nil {
		return
	}
	cmd.Err = cachedToolVersion(ctx, bin)
}

func cachedToolVersion(ctx context.Context, bin string) error {
	if _, ok := toolFloors[bin]; !ok {
		return nil
	}
	got, _ := toolProbes.LoadOrStore(bin, new(toolProbe))
	p := got.(*toolProbe)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return p.err
	}
	cache, err := probeToolVersion(ctx, bin)
	if !cache {
		return err
	}
	p.done = true
	p.err = err
	return err
}

// probeToolVersion runs the version command itself. It does not go through
// gitExec or vcsExec: those call back here, and git's pins are about a
// repository, which --version does not read.
//
// cache is false when ctx ended during the probe, so a cancelled call is not
// remembered as an answer about the binary, and when the binary is too old,
// since a server outlives the upgrade that fixes it.
func probeToolVersion(ctx context.Context, bin string) (cache bool, err error) {
	floor := toolFloors[bin]
	probeCtx, cancel := context.WithTimeout(ctx, toolVersionTimeout)
	defer cancel()
	out, runErr := exec.CommandContext(probeCtx, bin, floor.probe...).Output()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if runErr != nil {
		return true, nil
	}
	err = versionError(bin, string(out))
	return err == nil, err
}

// saplingClock is the build time Sapling appends to its date stamp
// (0.2.20260811-150444). It is not a prerelease.
var saplingClock = regexp.MustCompile(`(\d{8})-\d{6}`)

// versionError reports a parsed version below the floor. No version in the
// output is not a failure: an unreadable probe must not fail the run.
func versionError(bin, output string) error {
	floor, ok := toolFloors[bin]
	if !ok {
		return nil
	}
	if bin == "sl" {
		output = saplingClock.ReplaceAllString(output, "$1")
	}
	ver, ok := spells.ExtractVersion(output)
	if !ok {
		return nil
	}
	if (spells.VersionBounds{Min: floor.min}).Check(ver) != spells.VerdictTooOld {
		return nil
	}
	return types.DiagnosticErrorf(types.ToolTooOld,
		"%s %s is older than the supported range (min %s) for %s",
		bin, ver, floor.min, floor.flag)
}
