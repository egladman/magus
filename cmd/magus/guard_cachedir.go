package main

import (
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"mvdan.cc/sh/v3/syntax"
)

// The checkout's own magus cache dir, on both surfaces.
//
// Every marker in here is an INPUT to the verdicts the agent is graded by, so an agent
// that edits them rewrites its own evidence, and nothing in a later verdict says it
// happened. The rule therefore holds for every role, unbound sessions included: the lease
// rules protect a lane somebody handed out, this one protects the thing that decides
// whether any lane was checked at all.
//
// Reads are untouched, and the reader list below is what separates them. That list is
// where the rule fires wrongly, in two shapes worth knowing before reading a verdict: a
// command nobody listed as a reader is refused for merely NAMING a path in the dir
// (`echo .magus/logs/x.log`), and a `.magus` segment is matched wherever it appears, so a
// write into a DIFFERENT checkout's cache dir is refused here too. Both are the safe
// direction, and the denial says reading is fine.

// workspaceCacheDirName is the cache dir's default name, matched literally in addition to
// the resolved location.
//
// Both, because the two answer different failures. The resolved path is the real dir when
// MAGUS_CACHE_DIR or cache.dir has moved it; the literal name is what a command spells
// when the resolution is unavailable, which is any hook whose workspace magus could not
// locate. Matching only one of them leaves a deny that fails silently open.
const workspaceCacheDirName = ".magus"

// cacheDirSegmentRe matches the cache dir's name as a PATH SEGMENT anywhere in a word.
//
// Anywhere, not as a prefix, because the word is not always a path: an interpreter's
// inline script carries the path inside a program (`python3 -c "open('.magus/lease','a')"`),
// and a word whose value came from a parameter renders with the expansion dropped
// (`"$REPO/.magus/lease"` renders `/.magus/lease`), which resolves against nothing. A
// sibling name is still not a match: `.magus-notes` ends the segment with a character the
// pattern refuses.
var cacheDirSegmentRe = regexp.MustCompile(
	`(?:^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(workspaceCacheDirName) + `(?:/|$|[^A-Za-z0-9_./-])`)

// namesWorkspaceCacheDir reports whether a word points into the workspace's magus cache
// dir, either by resolving inside the dir magus located or by carrying its name as a path
// segment. A relative path is read against the directory the tool call RUNS in, which is
// what the shell would do, falling back to the workspace root when the host reported none.
func namesWorkspaceCacheDir(location hookActivityLocation, candidate string) bool {
	p := strings.TrimSpace(candidate)
	if p == "" {
		return false
	}
	if cacheDirSegmentRe.MatchString(filepath.ToSlash(p)) {
		return true
	}
	if location.base == "" {
		return false
	}
	abs := filepath.Clean(filepath.FromSlash(p))
	if !filepath.IsAbs(abs) {
		dir := cmp.Or(location.dir, location.workspace)
		if dir == "" {
			return false
		}
		abs = filepath.Join(dir, abs)
	}
	// Symlinks resolved on both sides, for the reason denyNotesWrite records: on macOS a
	// tmpdir-rooted workspace yields the dir under one spelling and the incoming path
	// under the other, and a deny that compares them literally looks enforced while
	// passing everything.
	rel, err := filepath.Rel(resolveSymlinks(location.base), resolveSymlinks(abs))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cacheDirDenial is the single text both surfaces refuse with, naming what was aimed at the
// dir. One text because the mistake is one mistake however it is spelled: a host's editor
// tool and a shell redirect reach the same bytes.
func cacheDirDenial(what string) string {
	return fmt.Sprintf("magus guard denied a write to %s, which is inside this checkout's magus cache dir. magus is the only writer of it.\n\n"+
		"That directory is not a pile of build leftovers any more. `%s` records which lease this checkout is bound to, `%s/` holds the fire-once advisory markers, the touched-project set, and the served-next journal whose entries pre-authorize commands, and the activity trail, run logs, outputs and locks sit beside them. The guard's verdicts are computed FROM those files, so editing one rewrites the evidence you are being graded by and no later verdict says so.\n\n"+
		"The verbs that do what you were probably after:\n"+
		"  `"+hint.SessionLease.With("<id>")+"` binds this checkout to a lease, and writes the marker for you.\n"+
		"  `"+hint.Clean.String()+"` removes the declared outputs.\n"+
		"  `"+hint.QueryOutput.With("<ref>")+"` prints a run's captured log.\n"+
		"`"+hint.Session.With("hook")+"` maintains its own markers and never needs you to edit them.\n"+
		"READING in there is fine; it is writing that belongs to magus.",
		what, ledger.LeaseMarkerName, advisoryMarkerDir)
}

// denyCacheDirPath is the path surface: the reason a file write into the cache dir is
// refused, or "" for every other path.
//
// Ranked above the lease rules in hookCmd, so a bound worker whose lane happens to cover
// the dir reads this rather than a lane verdict about the same path.
func denyCacheDirPath(location hookActivityLocation, writePath string) string {
	if !namesWorkspaceCacheDir(location, writePath) {
		return ""
	}
	return cacheDirDenial(strings.TrimSpace(writePath))
}

// cacheDirReaders are the commands that only READ what they are pointed at. Everything
// else is treated as a writer.
//
// A reader list rather than a writer list, which is the direction that fails safe: the
// eight-verb writer list this replaced let `dd of=`, `ln -sf`, `install`, `rmdir`,
// `chown`, `find -delete` and every inline interpreter write the served-next journal, and
// one line there stands down every role-scoped rule at once. magus is a reader by
// construction: every magus run writes in there, and the guard grades the agent's tool
// calls rather than magus's own processes.
var cacheDirReaders = map[string]bool{
	"cat": true, "bat": true, "head": true, "tail": true, "less": true, "more": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"jq": true, "wc": true, "ls": true, "stat": true, "file": true, "diff": true,
	"cut": true, "basename": true, "dirname": true, "realpath": true, "readlink": true,
	"du": true, "tree": true, "cd": true, "git": true,
	"sort": true, "awk": true, "find": true, "sed": true,
	"magus": true,
}

// cacheDirReads reports a command that only reads what it was pointed at. Four of the
// readers carry one spelling that turns them into writers, and a list that ignored those
// would be the writer allowlist again with the sides swapped.
func cacheDirReads(name string, args []string) bool {
	if !cacheDirReaders[name] {
		return false
	}
	switch name {
	case "sed":
		return !hasFlag(args, 'i', "in-place")
	case "sort":
		return !hasFlag(args, 'o', "output-file")
	case "awk":
		// awk's own language redirects, so the write is inside the program text rather
		// than on the shell line the parser walked.
		return !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, ">") })
	case "find":
		return !slices.ContainsFunc(args, func(a string) bool {
			return a == "-delete" || a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir"
		})
	}
	return true
}

// cacheDirScanDepth bounds how far a nested script is followed. A payload that parses to
// another payload shrinks on every hop, so the bound is for a line built not to.
const cacheDirScanDepth = 4

// denyCacheDirCommand is the command surface: the reason a shell line writes into the
// cache dir, or "" when it does not.
//
// Two shapes reach those bytes. A redirect names the file directly, and a command takes it
// as an operand. Both are read off the parsed line rather than matched as text, for the
// reason guard_shellparse.go's doc gives: a quoted string that merely NAMES the marker is
// not a write to it.
func denyCacheDirCommand(location hookActivityLocation, command string) string {
	hit := cacheDirCommandHit(location, command, 0)
	if hit == "" {
		return ""
	}
	return cacheDirDenial(hit)
}

// cacheDirCommandHit names the first path inside the cache dir the script would write, or
// "". Any `sh -c` or `eval` payload is scanned on its own afterwards: peelWrappers hands
// back the commands inside one, but a redirect there belongs to the inner parse tree and is
// invisible to a walk of the outer one.
func cacheDirCommandHit(location hookActivityLocation, command string, depth int) string {
	if depth > cacheDirScanDepth {
		return ""
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return ""
	}
	hit := ""
	var nested []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if hit != "" {
			return false
		}
		stmt, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		for _, r := range stmt.Redirs {
			if !writesToFile(r.Op) {
				continue
			}
			if target := literalWord(r.Word.Parts); namesWorkspaceCacheDir(location, target) {
				hit = target
				return false
			}
		}
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
			if script, ok := shellPayload(literalWords(call.Args)); ok {
				nested = append(nested, script)
			}
		}
		for _, c := range stmtCommands(stmt) {
			if target := cacheDirWriteTarget(location, c); target != "" {
				hit = target
				return false
			}
		}
		return true
	})
	if hit != "" {
		return hit
	}
	for _, script := range nested {
		if target := cacheDirCommandHit(location, script, depth+1); target != "" {
			return target
		}
	}
	return ""
}

// cacheDirWriteTarget names the path inside the cache dir that c would write, or "".
//
// Every word is examined, not only the operands: a flag's value names a path too
// (`dd of=.magus/lease`), and an interpreter's script is one argument carrying the path
// inside it.
func cacheDirWriteTarget(location hookActivityLocation, c guardCommand) string {
	name := path.Base(c.Name)
	if cacheDirReads(name, c.Args) {
		return ""
	}
	words := c.Args
	if name == "cp" || name == "install" {
		// Their operands are not uniform: only the destination is written, so copying a
		// log OUT of the dir is a read.
		ops := operands(c.Args, "m")
		if len(ops) < 2 {
			return ""
		}
		words = ops[len(ops)-1:]
	}
	// An interpreter's whole program arrives as one argument, awk's included, so a path
	// sits inside prose there and has to count. Everywhere else a word carrying
	// whitespace is prose rather than a path, which is what keeps `echo "rm -rf .magus"`
	// a quoted mention rather than a write.
	program := scriptedRewriteInterpreters[name] || name == "awk"
	i := slices.IndexFunc(words, func(w string) bool {
		if !program && strings.ContainsAny(w, " \t\n") {
			return false
		}
		return namesWorkspaceCacheDir(location, w)
	})
	if i < 0 {
		return ""
	}
	return words[i]
}

// rankCacheDirWrite ranks the cache-dir reason against the verdict the other command rules
// reached.
//
// It OUTRANKS an existing deny, which no other rule here does. `sed -i .magus/lease` earns
// the in-place refusal too, and that text sends the reader to an editor tool, which is the
// same write through the surface that would refuse it again.
func rankCacheDirWrite(v bashGuardVerdict, reason string) bashGuardVerdict {
	if reason == "" {
		return v
	}
	return bashGuardVerdict{Deny: reason, Rule: denyRule{Name: denyRuleCacheDirWrite}}
}
