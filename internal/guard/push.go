package guard

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// Refusing a push that no green gate covers.
//
// This is the one rule here that is a CONTRACT rather than a nudge, and the distinction
// is the reason it exists. The push advisory it replaces fired on every `git push`
// unconditionally: it never read the run log, so it told a caller who had just gated and a
// caller who had never gated exactly the same thing. A notice that cannot tell those two
// apart is not a reminder, it is a toll, and the measured response to a toll is to stop
// reading it.
//
// What makes a refusal legitimate here is that magus can PROVE the finding. The run log
// records, per invocation, the argv that started it, the per-target results, and the
// version string the binary was built from -- which carries the commit. So "no green `ci`
// has run at this commit" is a fact read off disk, not a suspicion. That is the line the
// doctrine draws: refuse what magus can prove, advise everything else.

// gateCoverage is what the run log says about the gate at one commit.
//
// ONE value, not a set of booleans. The states are mutually exclusive, and the first
// draft encoded them as three flags, which made illegal combinations expressible
// (unknown-and-passed) and immediately produced the bug they invite: the zero value read
// as "no gate ran" when it meant "nothing was asked", and the rule denied every push in a
// fresh clone. A reader also cannot tell from a bool pair which combinations are real.
type gateCoverage int

const (
	// gateUnknown is the question not being ASKABLE: no run log to read, or no commit to
	// match against. The zero value, and it must never deny: it is the state of a fresh
	// clone, a host with no VCS, and every test fixture.
	gateUnknown gateCoverage = iota
	// gateAbsent is a readable run log holding no gate run at this commit.
	gateAbsent
	// gateFailed is a gate run at this commit that FINISHED and did not pass.
	gateFailed
	// gateIncomplete is a gate run at this commit that started and wrote no finished
	// event: still running, or killed. The log cannot tell those apart -- both are simply
	// a missing record -- so this names the superset rather than claiming the one it
	// cannot prove. Asking the project lock would distinguish them, which is machinery
	// this rule does not need: neither is coverage.
	gateIncomplete
	// gatePassed is a gate run at this commit that finished with every target passing.
	gatePassed
)

// gateCoverageAt reads the run log for gate invocations built from commit.
//
// COMMIT, not tree state. An exact tree match is available -- the checkpoint digest would
// give it -- and it is the wrong threshold: it invalidates on the first comment typo after
// a green gate, which is the case the cadence explicitly allows and the case a caller
// would route around the rule to get. A commit with no green gate at all is the failure
// worth refusing, and it is the one people actually ship.
//
// Empty commit, or no run log, reports nothing known: this rule stands down rather than
// refusing on an absence it cannot account for.
func gateCoverageAt(runsDir, commit string) gateCoverage {
	if runsDir == "" || commit == "" {
		return gateUnknown
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return gateUnknown
	}
	out := gateAbsent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch readGateOutcome(filepath.Join(runsDir, e.Name()), commit) {
		case gatePassed:
			return gatePassed
		// A failure outranks an incomplete run when both are present: it is the more
		// definite thing to tell the caller, and the message differs.
		case gateFailed:
			out = gateFailed
		case gateIncomplete:
			if out == gateAbsent {
				out = gateIncomplete
			}
		}
	}
	return out
}

// gatePassStatus is the overall outcome a finished gate writes. Journal statuses are
// plain strings on the wire (see journal.Invocation.Status), so this names the one value
// that counts rather than comparing a literal at the call site.
const gatePassStatus = "pass"

// readGateOutcome reports whether one run log is a gate invocation built from commit that
// finished passing, and whether it was a gate invocation at this commit at all.
//
// Folded through journal.InvocationFromEvents rather than by reading the two lifecycle
// events here: that function already knows which event carries the command, the version
// and the status, and a second reading of the same stream is a second thing to keep true.
// It also supplies the interrupted case for free -- a run killed at the terminal writes no
// finished event, so Status stays empty and reads as not-green, which is correct.
//
// The file is bounded before it is parsed. A run log holds every line a build emitted, so
// the biggest here run to megabytes, and this is called once per log on a push.
func readGateOutcome(path, commit string) gateCoverage {
	f, err := os.Open(path)
	if err != nil {
		return gateUnknown
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), gateLogLineMax)
	var events []journal.Event
	for sc.Scan() {
		var ev journal.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		// The started event is the first line, so a log that is not this commit's gate
		// is abandoned on line one instead of being read to the end.
		if ev.Kind == journal.KindStarted {
			if ev.Command == nil || !isGateCommand(ev.Command.Arguments) || !builtFrom(ev.MagusVersion, commit) {
				return gateUnknown
			}
		}
		if ev.Kind == journal.KindStarted || ev.Kind == journal.KindFinished {
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		return gateUnknown
	}
	switch journal.InvocationFromEvents("", events).Status {
	case gatePassStatus:
		return gatePassed
	// No status at all means no finished event: the run was killed, or is still going.
	case "":
		return gateIncomplete
	default:
		return gateFailed
	}
}

// gateLogLineMax bounds one line of a run log. A build's captured output lands here, so a
// line can be long; past this it is not a journal event any reader here understands.
const gateLogLineMax = 4 << 20

// builtFrom reports whether a magus version string names this commit.
//
// `git describe` renders it as v0.4.3-97-g4a1c0cc84, optionally with -dirty, so the
// commit appears as a g-prefixed abbreviation. Compared by PREFIX in both directions
// because the two sides abbreviate to different lengths: the describe output picks its
// own, and the caller passes whatever `vcs.Commit().short` gave it.
func builtFrom(version, commit string) bool {
	if version == "" || commit == "" {
		return false
	}
	for _, field := range strings.Split(version, "-") {
		abbrev, ok := strings.CutPrefix(field, "g")
		if !ok || abbrev == "" {
			continue
		}
		if strings.HasPrefix(commit, abbrev) || strings.HasPrefix(abbrev, commit) {
			return true
		}
	}
	return false
}

// denyPushWithoutGate is the verdict for a push at a commit no green gate covers, or ""
// when one does.
//
// It names the deliberate-publish escape, and that is not a weakness in the rule. A
// work-in-progress push is legitimate and magus cannot tell one from an oversight, so the
// rule refuses the SILENT case and lets a caller who says what they are doing through. A
// deny with no way past it is a deny people disable.
func denyPushWithoutGate(cover gateCoverage) string {
	var state string
	switch cover {
	// Unknown passes. This is the only deny in its tier, so it refuses on evidence or not
	// at all: no run log, no VCS, or a host magus cannot read the revision from all mean
	// the question was never asked, and a refusal there is one nobody can clear.
	case gateUnknown, gatePassed:
		return ""
	case gateAbsent:
		state = "no `" + string(types.TargetCI) + "` run is recorded at this commit"
	case gateFailed:
		state = "the `" + string(types.TargetCI) + "` run at this commit failed"
	case gateIncomplete:
		state = "the `" + string(types.TargetCI) + "` run at this commit never finished, so it is still going or it was killed"
	}
	return "pushing a commit no passing gate covers: " + state + ".\n" +
		"`" + hint.Affected.With(string(types.TargetCI)) + "` runs it, and CI runs the identical command on the identical tree, so a red push pays twice for one answer.\n" +
		"Publishing work-in-progress deliberately? Say so and push again."
}
