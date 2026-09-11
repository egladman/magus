package main

import (
	"cmp"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
)

// Pre-authorization: a command magus itself served as the next step.
//
// Eli ruled on 2026-09-11 that trusting one is not an escape, and the premise it
// overturns is worth stating, because it reads like one: the worker chose nothing here.
// magus computed the command, printed it as a breadcrumb, and the agent ran it verbatim.
// Grading magus's own suggestion against a role's boundary and refusing it says the tool
// disagrees with itself, and the reader has no way to tell which half to believe.
//
// The obligation moves UPSTREAM instead. `next` is computed for the acting role, and
// TestEveryServedNextPassesTheGuard grades every template the tree can serve, so a
// suggestion magus would refuse cannot ship. This half is the reader.

// servedNextJournal is the journal the serving side appends to, one JSON line per entry,
// beside the advisory markers and keyed by the same session hash. Absent means nothing is
// pre-authorized, which is the only safe reading of a file that is not there.
const servedNextJournal advisoryKind = "served-next"

// servedNextWindow is how many recent entries stay pre-authorized.
//
// A bound rather than a clock: what makes a served command trustworthy is that it was
// served to THIS session about THIS state, and a session that has run twenty commands
// since is somewhere else. Small enough that a stale suggestion ages out, large enough
// to cover a result carrying the cap of three.
const servedNextWindow = 20

// servedNextEntry is one line of the journal, the shape the serving side writes. The
// reader here is deliberately narrower than hint.ReadServedNext: it wants this session's
// file and the anonymous one, not every session's, and it skips a line naming no template.
type servedNextEntry = hint.ServedNextEntry

// servedNextPreauthorizes names the template that served command to this session, or ""
// when nothing did.
//
// The id IS the answer rather than a bool beside one, because the metric this feeds counts
// uptake per template: a pre-authorization nobody can attribute is one nobody can prune by
// the number, which is the whole argument for `next` carrying stable ids.
//
// Exactly one command on the line. A served next is a single invocation, so a line that
// chains one with anything else is not the thing that was served, and clearing it would
// pre-authorize whatever was chained on.
func servedNextPreauthorizes(gate advisoryGate, command string) string {
	if gate.base == "" {
		return ""
	}
	cmds, ok := parseGuardCommands(command)
	if !ok || len(cmds) != 1 {
		return ""
	}
	argv := normalizeServedArgv(append([]string{cmds[0].Name}, cmds[0].Args...))
	if len(argv) == 0 {
		return ""
	}
	for _, entry := range readServedNext(gate) {
		if slices.Equal(normalizeServedArgv(entry.Argv), argv) {
			return entry.ID
		}
	}
	return ""
}

// servedNextPath names one journal, through markerPath so the hashing cannot drift from
// the advisory markers the serving side writes beside.
func (g advisoryGate) servedNextPath() string { return g.markerPath(servedNextJournal) }

// readServedNext returns the newest servedNextWindow entries this session may have been
// served, and nothing at all when no journal is readable.
//
// TWO journals, merged by timestamp. A hook call reports a session id and reads the file
// keyed to it; a CLI run carries no session id at all, so `next` printed by `magus query`
// lands in the anonymous bucket, and reading only the keyed file would pre-authorize
// nothing an agent actually saw. The cost is that the anonymous journal is shared by every
// session in the checkout, so a command served to one can clear it for another running
// beside it.
//
// Unparsable lines are SKIPPED rather than ending the read: the journal is appended to by
// a concurrent process, so a torn final line is ordinary, and abandoning the file over one
// would silently stop pre-authorizing everything behind it. An entry naming no template is
// skipped with them: the id is what makes the clearance countable, and one that cannot be
// attributed is a clearance nobody can audit or prune.
func readServedNext(gate advisoryGate) []servedNextEntry {
	paths := []string{gate.servedNextPath()}
	if anon := (advisoryGate{base: gate.base}).servedNextPath(); anon != paths[0] {
		paths = append(paths, anon)
	}
	var entries []servedNextEntry
	for _, path := range paths {
		entries = append(entries, readServedNextFile(path)...)
	}
	slices.SortStableFunc(entries, func(a, b servedNextEntry) int { return cmp.Compare(a.Ts, b.Ts) })
	if len(entries) > servedNextWindow {
		entries = entries[len(entries)-servedNextWindow:]
	}
	return entries
}

// readServedNextFile reads one journal, oldest entry first.
func readServedNextFile(path string) []servedNextEntry {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	entries := make([]servedNextEntry, 0, len(lines))
	for _, line := range lines {
		var entry servedNextEntry
		if json.Unmarshal([]byte(line), &entry) != nil || len(entry.Argv) == 0 || entry.ID == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// normalizeServedArgv reduces an argv to the form the two sides can agree on: the binary
// by its base name, everything after it verbatim.
//
// The spelling of the binary is the one thing that legitimately differs between what was
// printed and what was run (`./magus` in a worktree, `magus` on PATH, an absolute path
// from a hook template), and it is the one part of the line that changes nothing about
// what the command does.
func normalizeServedArgv(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	out := slices.Clone(argv)
	out[0] = path.Base(strings.ReplaceAll(out[0], "\\", "/"))
	return out
}
