package main

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"mvdan.cc/sh/v3/syntax"
)

// The checkout's own magus cache dir, on both surfaces.
//
// What lives in there is no longer a pile of regenerable artifacts. The `lease` marker
// says which lease this checkout is bound to, and writing it directly is how the store's
// one-way BindLease gets bypassed. The advisory markers decide which notices a session
// has already been told, and the served-next journal beside them pre-authorizes the
// commands magus suggested. Every one of those is an INPUT to the verdicts the agent is
// graded by, so an agent that edits them rewrites its own evidence, and nothing in a later
// verdict says it happened.
//
// So the rule holds for every role, unbound sessions included. The lease rules protect a
// lane somebody handed out; this one protects the thing that decides whether any lane was
// checked at all, which is nobody's lane to edit.
//
// Reads are untouched. A log in there is ordinary text and a person or an agent may cat it.

// workspaceCacheDirName is the cache dir's default name, matched literally in addition to
// the resolved location.
//
// Both, because the two answer different failures. The resolved path is the real dir when
// MAGUS_CACHE_DIR or cache.dir has moved it; the literal name is what a command spells
// when the resolution is unavailable, which is any hook whose workspace magus could not
// locate. Matching only one of them leaves a deny that fails silently open.
const workspaceCacheDirName = ".magus"

// inWorkspaceCacheDir reports whether candidate lands inside the workspace's magus cache
// dir. Relative paths are read against root, which is where the literal `.magus/` spelling
// means what it says.
//
// A sibling name is not a match: `.magus-notes/x` is neither the dir nor under it, on
// either half of the check.
func inWorkspaceCacheDir(root, cacheDir, candidate string) bool {
	p := strings.TrimSpace(candidate)
	if p == "" {
		return false
	}
	p = filepath.Clean(filepath.FromSlash(p))

	rel := p
	if filepath.IsAbs(p) {
		rel = ""
		if root != "" {
			if r, err := filepath.Rel(root, p); err == nil {
				rel = r
			}
		}
	}
	if rel == workspaceCacheDirName || strings.HasPrefix(rel, workspaceCacheDirName+string(filepath.Separator)) {
		return true
	}

	if cacheDir == "" {
		return false
	}
	abs := p
	if !filepath.IsAbs(abs) {
		if root == "" {
			return false
		}
		abs = filepath.Join(root, abs)
	}
	// Symlinks resolved on both sides, for the reason denyNotesWrite records: on macOS a
	// tmpdir-rooted workspace yields the dir under one spelling and the incoming path
	// under the other, and a deny that compares them literally looks enforced while
	// passing everything.
	r, err := filepath.Rel(resolveSymlinks(cacheDir), resolveSymlinks(abs))
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// cacheDirDeny is the single text both surfaces refuse with, naming what was aimed at the
// dir. One text because the mistake is one mistake however it is spelled: a host's editor
// tool and a shell redirect reach the same bytes.
func cacheDirDeny(what string) string {
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

// denyCacheDirWrite is the path surface: the reason a file write into the cache dir is
// refused, or "" for every other path.
//
// Ranked above the lease rules in hookCmd, so a bound worker whose lane happens to cover
// the dir reads this rather than a lane verdict about the same path.
func denyCacheDirWrite(root, cacheDir, writePath string) string {
	if !inWorkspaceCacheDir(root, cacheDir, writePath) {
		return ""
	}
	return cacheDirDeny(strings.TrimSpace(writePath))
}

// cacheDirWriters are the coreutils verbs whose OPERANDS are things they write. A magus
// argv is deliberately absent and cannot be added: every magus run writes in there, and
// the guard grades the agent's tool calls rather than magus's own processes.
//
// cp is the one whose operands are not uniform, so it is judged on its destination alone
// below; copying a log OUT of the dir is a read.
var cacheDirWriters = map[string]bool{
	"rm": true, "mv": true, "tee": true, "truncate": true,
	"chmod": true, "mkdir": true, "touch": true,
}

// denyCacheDirCommand is the command surface: the reason a shell line writes into the
// cache dir, or "" when it does not.
//
// Two shapes reach those bytes. A redirect names the file directly, and a coreutil takes
// it as an operand. Both are read off the parsed line rather than matched as text, for the
// reason guard_shellparse.go's doc gives: a quoted string that merely NAMES the marker is
// not a write to it.
func denyCacheDirCommand(command, root, cacheDir string) string {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return ""
	}
	hit := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if hit != "" {
			return false
		}
		stmt, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		for _, r := range stmt.Redirs {
			switch r.Op {
			case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll:
				if target := literalWord(r.Word.Parts); inWorkspaceCacheDir(root, cacheDir, target) {
					hit = target
					return false
				}
			}
		}
		for _, c := range stmtCommands(stmt) {
			if target := cacheDirWriteTarget(c, root, cacheDir); target != "" {
				hit = target
				return false
			}
		}
		return true
	})
	if hit == "" {
		return ""
	}
	return cacheDirDeny(hit)
}

// cacheDirWriteTarget names the path inside the cache dir that c would write, or "".
func cacheDirWriteTarget(c guardCommand, root, cacheDir string) string {
	name := path.Base(c.Name)
	// truncate -s, mkdir -m and chmod's reference form all consume the next word, and a
	// size or a mode is not a path this would match anyway; -R and -f take none.
	ops := operands(c.Args, "sm")
	switch {
	case name == "sed":
		if !hasFlag(c.Args, 'i', "in-place") {
			return ""
		}
	case name == "cp":
		if len(ops) < 2 {
			return ""
		}
		ops = ops[len(ops)-1:]
	case !cacheDirWriters[name]:
		return ""
	}
	i := slices.IndexFunc(ops, func(op string) bool { return inWorkspaceCacheDir(root, cacheDir, op) })
	if i < 0 {
		return ""
	}
	return ops[i]
}

// rankCacheDirWrite ranks the cache-dir reason against the verdict the other command rules
// reached.
//
// It OUTRANKS an existing deny, which no other rule here does. `sed -i .magus/lease` earns
// the in-place refusal too, and that text sends the reader to an editor tool, which is the
// same write through the surface that would refuse it again. Saying so once is the whole
// benefit of having two surfaces agree.
func rankCacheDirWrite(v bashGuardVerdict, reason string) bashGuardVerdict {
	if reason == "" {
		return v
	}
	return bashGuardVerdict{Deny: reason, Rule: denyRule{Name: denyRuleCacheDirWrite}}
}
