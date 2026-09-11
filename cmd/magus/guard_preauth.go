package main

import (
	"os"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
)

// Pre-authorization: a command magus itself served as the next step.
//
// Trusting one is not an escape: the worker chose nothing here. magus computed the
// command, printed it as a breadcrumb, and the agent ran it verbatim. Grading magus's
// own suggestion against a role's boundary and refusing it says the tool disagrees
// with itself, and the reader has no way to tell which half to believe.
//
// The obligation moves UPSTREAM instead: `next` is computed for the acting role, and
// TestEveryServedNextPassesTheGuardForEveryRole grades every template the tree can
// serve. This half is the reader.

// servedNextWindow is how many recent entries stay pre-authorized.
//
// A bound rather than a clock: what makes a served command trustworthy is that it was
// served about THIS state, and a session that has run twenty commands since is
// somewhere else. Small enough that a stale suggestion ages out, large enough to cover
// a result carrying the cap of three.
const servedNextWindow = 20

// servedNextEntry is one line of the journal, the shape the serving side writes.
type servedNextEntry = hint.ServedNextEntry

// servedNextPreauthorizes names the template that served command in this checkout, or
// "" when nothing did.
//
// The id IS the answer rather than a bool beside one, because the metric this feeds
// counts uptake per template: a pre-authorization nobody can attribute is one nobody
// can prune by the number, which is the whole argument for `next` carrying stable ids.
//
// Exactly one command on the line, spelled as it was served. A line that chains a
// second command, redirects, or wraps the served argv in sudo/env/sh -c is not the
// thing that was served: clearing it would pre-authorize whatever rode along.
//
// The journal is per CHECKOUT, so a command served to one session clears it for
// another running beside it in the same tree.
func servedNextPreauthorizes(gate advisoryGate, command string) string {
	if gate.base == "" {
		return ""
	}
	argv := hint.NormalizeServedArgv(servedArgv(command))
	if len(argv) == 0 {
		return ""
	}
	for _, entry := range readServedNext(gate) {
		if slices.Equal(hint.NormalizeServedArgv(entry.Argv), argv) {
			return entry.ID
		}
	}
	return ""
}

// readServedNext returns the newest servedNextWindow entries this checkout served,
// and nothing at all when no journal is readable.
//
// Unparsable lines are SKIPPED rather than ending the read: the journal is appended to
// by a concurrent process, so a torn final line is ordinary, and abandoning the file
// over one would silently stop pre-authorizing everything behind it. An entry naming
// no template is skipped with them: the id is what makes the clearance countable, and
// one that cannot be attributed is a clearance nobody can audit or prune.
func readServedNext(gate advisoryGate) []servedNextEntry {
	entries := readServedNextFile(hint.ServedNextPath(gate.base))
	if len(entries) > servedNextWindow {
		entries = entries[len(entries)-servedNextWindow:]
	}
	return entries
}

// readServedNextFile reads one journal, oldest entry first.
func readServedNextFile(journal string) []servedNextEntry {
	body, err := os.ReadFile(journal)
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

// servedArgv renders command as the single argv it would run, or nil when the line is
// anything else.
//
// Deliberately NOT parseGuardCommands: that one peels sudo/env/sh -c to find the
// program a line really runs, which is right for judging a line and wrong for matching
// one against a suggestion. `sudo magus affected ci` peels to the served argv while
// running as root, and a redirect the guard's peeling ignores turns a read into a
// write of the reader's choosing. Both have to miss.
func servedArgv(command string) []string {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(f.Stmts) != 1 {
		return nil
	}
	stmt := f.Stmts[0]
	if len(stmt.Redirs) > 0 || stmt.Background || stmt.Negated {
		return nil
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return nil
	}
	argv := make([]string, 0, len(call.Args))
	for _, w := range call.Args {
		word, ok := literalArg(w.Parts)
		if !ok {
			return nil
		}
		argv = append(argv, word)
	}
	return argv
}

// literalArg renders a word the shell would pass verbatim. False for a word carrying
// an expansion: its value is not knowable here, and a served argv has none.
func literalArg(parts []syntax.WordPart) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			inner, ok := literalArg(p.Parts)
			if !ok {
				return "", false
			}
			b.WriteString(inner)
		default:
			return "", false
		}
	}
	return b.String(), true
}
