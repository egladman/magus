package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The SPLIT-RUN advisory: `magus run` (and `magus affected`) takes one target and many
// projects, so the same target run twice on two different project sets is usually the one
// call typed as two. Two shapes reach here:
//
//   - ON ONE LINE (`magus run lint . && magus run lint docs`): chainedRunRe already flags
//     this as a chain worth questioning; splitRunLineAdvice narrows its TEXT to the combined
//     form when both sides share a target, and leaves chainedRunRe's general text in place
//     when they do not (that is still its domain, per advisoryChainedRun).
//   - ACROSS TWO CALLS: Evaluate sees one line at a time and remembers nothing between
//     them, so this rung reads and writes a per-session fact instead: the target and
//     project set of the last magus run/affected invocation. gradeScopeDrift's
//     touched-projects file is the precedent for this, on the SAME `facts` gate guard.go
//     already keys on the caller's facts (FactsKey) rather than its rendered text, so no new store
//     is needed.

// splitRunWindow bounds how far apart two calls may be and still read as one mistake
// rather than two unrelated runs. Ten minutes covers a rushed pair of back-to-back tool
// calls in one turn -- the shape this catches -- without tying together two runs of the
// same target hours apart in a long session, which is ordinary repeated work rather than
// a split that should have been one call.
const splitRunWindow = 10 * time.Minute

// factsLastRun names the file holding the last magus run/affected invocation this session
// made. A marker KIND rather than a notice, like advisoryTouchedProjects beside it, so
// hint.MarkerPath keys it on the same hashed session id and the retention sweep that runs
// beside the advisory markers reaps it too.
const factsLastRun hint.MarkerKind = "run-invocation"

// splitRunSep joins a recorded invocation's fields. Not a comma: a project path or a
// charm list never carries this byte, where either could carry a comma.
const splitRunSep = "\x1f"

// lastRunFact is one recorded magus run/affected invocation, plus how long ago it was
// recorded.
type lastRunFact struct {
	verb     string
	id       string // canonical target identity: name, or name:charm,charm
	raw      string // the target token as typed, for rendering a faithful command back
	projects []string
	age      time.Duration
}

// gradeSplitRun compares the current command's magus run/affected invocation against the
// one this session last recorded, and reports the advice owed for a same-target,
// different-project pair inside the window.
//
// It records the CURRENT call unconditionally, whatever it decides: the next call must
// compare against this one, not a stale pair, the same reasoning scopeDrift.record()
// documents for touched-projects.
//
// Silent on every uncertainty: no cache dir, a line that is not a single magus
// run/affected call (a chain is splitRunLineAdvice's shape, in Evaluate), or a target
// string the target grammar rejects.
func gradeSplitRun(facts hint.Gate, command string) (advice string, ok bool) {
	if facts.CacheDir() == "" {
		return "", false
	}
	cmds, parsed := ParseCommands(command)
	if !parsed {
		return "", false
	}
	verb, raw, projects, found := singleMagusRunInvocation(cmds)
	if !found {
		return "", false
	}
	id, idOK := targetIdentity(raw)
	if !idOK {
		return "", false
	}
	path := hint.MarkerPath(facts.CacheDir(), facts.Session(), factsLastRun)
	prev, hadPrev := readLastRun(path)
	writeLastRun(path, verb, id, raw, projects)
	if !hadPrev || prev.age > splitRunWindow || prev.verb != verb || prev.id != id {
		return "", false
	}
	if slices.Equal(sortedCopy(prev.projects), sortedCopy(projects)) {
		// The same call again: nothing to combine.
		return "", false
	}
	cmdA := renderMagusCmd(prev.verb, prev.raw, prev.projects)
	cmdB := renderMagusCmd(verb, raw, projects)
	combined := renderMagusCmd(verb, raw, mergeProjects(prev.projects, projects))
	return splitRunAdvice(cmdA, cmdB, combined), true
}

// singleMagusRunInvocation reports the one magus run/affected invocation on this line,
// and false when the line carries none or more than one: a line chaining two is
// splitRunLineAdvice's shape, not this rung's, and conflating them would record a fact
// from the losing half of a chain the pure rule already answered.
func singleMagusRunInvocation(cmds []hint.Invocation) (verb, raw string, projects []string, ok bool) {
	count := 0
	for _, c := range cmds {
		if c.Name != "magus" {
			continue
		}
		if v, t, p, found := splitRunInvocation(c.Args); found {
			verb, raw, projects = v, t, p
			count++
		}
	}
	return verb, raw, projects, count == 1
}

// splitRunLineAdvice narrows a ONE-LINE chain (already matched by chainedRunRe) to the
// combined-run text when every magus run/affected invocation on the line names the same
// target: charms included, since `lint` and `lint:rw` are different targets and combining
// them would silently drop the charm. False falls back to the general chained-run text,
// which stays correct for a chain of genuinely different targets.
func splitRunLineAdvice(cmds []hint.Invocation) (string, bool) {
	type call struct {
		verb, id, raw string
		projects      []string
	}
	var calls []call
	for _, c := range cmds {
		if c.Name != "magus" {
			continue
		}
		v, raw, p, found := splitRunInvocation(c.Args)
		if !found {
			continue
		}
		id, idOK := targetIdentity(raw)
		if !idOK {
			continue
		}
		calls = append(calls, call{v, id, raw, p})
	}
	if len(calls) < 2 {
		return "", false
	}
	first := calls[0]
	merged := slices.Clone(first.projects)
	for _, c := range calls[1:] {
		if c.verb != first.verb || c.id != first.id {
			return "", false
		}
		merged = mergeProjects(merged, c.projects)
	}
	cmdA := renderMagusCmd(first.verb, first.raw, first.projects)
	cmdB := renderMagusCmd(calls[1].verb, calls[1].raw, calls[1].projects)
	combined := renderMagusCmd(first.verb, first.raw, merged)
	return splitRunAdvice(cmdA, cmdB, combined), true
}

// splitRunAdvice names both original commands and the one call that already covers them.
// It leads with the replacement, this file's convention for text that carries a command:
// the reader chose to keep going and is owed the runnable answer first.
func splitRunAdvice(cmdA, cmdB, combined string) string {
	return fmt.Sprintf(
		"`%s` and `%s` run the same target on separate projects. `%s` runs both in one invocation, "+
			"with one workspace load and one scheduler, parallel where the graph allows and ordered "+
			"when one project reads another's outputs.",
		cmdA, cmdB, combined)
}

// renderMagusCmd renders one magus run/affected invocation as the reader would type it,
// target and charms exactly as given.
func renderMagusCmd(verb, target string, projects []string) string {
	args := append([]string{target}, projects...)
	if verb == "affected" {
		return hint.Affected.With(args...)
	}
	return hint.Run.With(args...)
}

// mergeProjects unions two project sets, A's order first, deduplicated: the combined
// command a caller reads back should name each project once.
func mergeProjects(a, b []string) []string {
	out := slices.Clone(a)
	for _, p := range b {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// magusRunValueFlags are magus's own global flags that consume a separate operand,
// mirrored from cmd/magus's peekSub (which this package cannot import: main is a command,
// not a library). Missing an entry here costs a missed advisory, not a wrong one: the next
// bare word is misread as the verb and parsing simply stops rather than misfiring.
var magusRunValueFlags = map[string]bool{
	"-root": true, "--root": true, "-C": true, "--C": true,
	"-config": true, "--config": true, "-c": true, "--c": true,
}

// splitRunInvocation reads the verb, target token and projects off one magus
// invocation's own arguments (the words after "magus"), the same shape peekSub resolves
// in cmd/magus: a global flag may precede the verb as `--flag=value` (one token) or
// `--flag value` (two), and a bare boolean flag is one token. False for anything that is
// not a `run` or `affected` call, or whose target position is itself a flag.
func splitRunInvocation(args []string) (verb, target string, projects []string, ok bool) {
	i := skipMagusFlags(args, 0)
	if i >= len(args) || (args[i] != "run" && args[i] != "affected") {
		return "", "", nil, false
	}
	verb = args[i]
	i = skipMagusFlags(args, i+1)
	if i >= len(args) || args[i] == "" || args[i][0] == '-' {
		return "", "", nil, false
	}
	target = args[i]
	i++
	for i < len(args) {
		a := args[i]
		if a == "" || a == "--" || a[0] == '-' {
			break
		}
		projects = append(projects, a)
		i++
	}
	return verb, target, projects, true
}

// skipMagusFlags advances past magus's own global flags starting at i, stopping at the
// first bare word.
func skipMagusFlags(args []string, i int) int {
	for i < len(args) {
		a := args[i]
		if a == "" || a[0] != '-' {
			return i
		}
		if strings.ContainsRune(a, '=') {
			i++
			continue
		}
		if magusRunValueFlags[a] && i+1 < len(args) {
			i += 2
			continue
		}
		i++
	}
	return i
}

// targetIdentity canonicalizes a target token (name plus charms) the way isGateCommand's
// ParseTarget call does, so `lint` and `lint:rw` compare as different targets and charm
// order never does (`lint:a,b` and `lint:b,a` are the same target).
func targetIdentity(raw string) (string, bool) {
	t, err := types.ParseTarget(raw)
	if err != nil {
		return "", false
	}
	if len(t.Charms) == 0 {
		return t.Name, true
	}
	charms := sortedCopy(t.Charms)
	return t.Name + ":" + strings.Join(charms, ","), true
}

// readLastRun reads the fact file at path, and how long ago it was written (the file's
// own mtime, rather than a stored timestamp: one fewer field to parse and keep in sync
// with the write).
func readLastRun(path string) (lastRunFact, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return lastRunFact{}, false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return lastRunFact{}, false
	}
	parts := strings.Split(string(body), splitRunSep)
	if len(parts) < 3 {
		return lastRunFact{}, false
	}
	fact := lastRunFact{verb: parts[0], id: parts[1], raw: parts[2], age: time.Since(info.ModTime())}
	if len(parts) > 3 {
		fact.projects = parts[3:]
	}
	return fact, true
}

// writeLastRun overwrites the fact file with the current invocation. Best-effort, like
// scopeDrift.record(): a set magus cannot write means the rule speaks again later, the
// smaller failure.
func writeLastRun(path, verb, id, raw string, projects []string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	fields := append([]string{verb, id, raw}, projects...)
	_ = os.WriteFile(path, []byte(strings.Join(fields, splitRunSep)), 0o644)
}
