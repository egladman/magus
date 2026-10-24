package guard

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// A script rewriting a file that holds merge conflict markers.
//
// interpreter-rewrite advises by default, and a workspace raises it to deny in its own
// policy. Mid-merge that policy is often the thing that does not load, since its files are
// conflicted too, so the raise is gone exactly when a script is most likely to resolve hunks
// by rewriting a file wholesale. A script that opens the file for writing and then raises
// leaves it empty. This rule denies by default so it holds without the workspace.

// conflictStartRe and conflictEndRe match the markers git writes around a hunk: seven
// characters, then a label or the end of the line.
var (
	conflictStartRe = regexp.MustCompile(`(?m)^<{7}(?:[ \r]|$)`)
	conflictEndRe   = regexp.MustCompile(`(?m)^>{7}(?:[ \r]|$)`)
)

// conflictScanBytes bounds the read of one candidate; a hook has seconds, and a source
// file past it is not one a merge left markers in by hand.
const conflictScanBytes = 8 << 20

// conflictScript is the script resolveCommand serves, when the workspace carries it.
const conflictScript = "hack/dev/resolve-conflicts.buzz"

// holdsConflictMarkers reports whether file opens and closes a conflict hunk.
func holdsConflictMarkers(file string) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, conflictScanBytes))
	if err != nil {
		return false
	}
	return conflictStartRe.Match(b) && conflictEndRe.Match(b)
}

// conflictedWorkspaceFile names the first candidate that is a file in the workspace holding
// conflict markers, or "".
func conflictedWorkspaceFile(workspace string, candidates []string) string {
	for _, c := range candidates {
		rel := existingWorkspaceFile(workspace, []string{c})
		if rel != "" && holdsConflictMarkers(filepath.Join(workspace, rel)) {
			return rel
		}
	}
	return ""
}

// resolveCommand is the golden path for file's hunks, or "" when the workspace does not
// carry the script.
func resolveCommand(workspace, file string) string {
	if workspace == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(workspace, conflictScript)); err != nil {
		return ""
	}
	return hint.Buzz.With(conflictScript, "--", "--file", file)
}

// denyConflictRewrite is the reason an inline interpreter would rewrite a file holding
// conflict markers, or "" when it would not.
//
// Two shapes are certain. A write whose destination is such a file, read the way
// interpreter-rewrite reads destinations. And a write whose destination this cannot read,
// from a program that carries a conflict marker: it parses hunks and writes somewhere it does
// not name, which is resolving conflicts by script. A destination it cannot read also counts
// any conflicted file the program names as a literal.
func denyConflictRewrite(location location, command string, d Dialect) string {
	if location.workspace == "" {
		return ""
	}
	f, err := parseFile(command, d)
	if err != nil {
		return ""
	}
	hit, found := "", false
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
			return false
		}
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok {
			return true
		}
		for _, c := range peelWrappers(literalWords(call.Args), d) {
			if hit, found = conflictRewriteTarget(location.workspace, c, heredocText(st)); found {
				return false
			}
		}
		return true
	})
	if !found || rewriteStaysOutside(scopeAt(location), command, d) {
		return ""
	}
	return conflictRewriteDenial(location.workspace, hit)
}

// conflictRewriteTarget names the conflicted file c's program would write, "" with found
// true when it would write one it does not name.
func conflictRewriteTarget(workspace string, c hint.Invocation, heredoc string) (hit string, found bool) {
	name := path.Base(c.Name)
	if name == "magus" {
		script, ok := inlineBuzz(c.Args, heredoc)
		if !ok {
			return "", false
		}
		hit = conflictedWorkspaceFile(workspace, buzzWriteDestinations(script))
		return hit, hit != ""
	}
	if !scriptedRewriteInterpreters[name] && name != "awk" {
		return "", false
	}
	script := interpreterScript(c.Args, heredoc)
	if !scriptWrites(name, script, c.Args) {
		return "", false
	}
	candidates, unknown := writeCandidates(name, script, c.Args)
	// Literals that name no path, such as the argv of the git call a loop reads its files
	// from, leave the destination as unread as no literal at all. A destination under a
	// scratch path leaves no candidate, and is known.
	unknown = unknown || len(candidates) > 0 && !slices.ContainsFunc(candidates, namesAPath)
	if unknown {
		candidates = append(append(candidates, c.Args...), quotedLiterals(script)...)
	}
	if hit = conflictedWorkspaceFile(workspace, candidates); hit != "" {
		return hit, true
	}
	return "", unknown && strings.Contains(script, "<<<<<<<")
}

// namesAPath reports a literal that could be a file path rather than a flag or a word.
func namesAPath(s string) bool {
	return strings.ContainsAny(s, "./") && !strings.HasPrefix(s, "-")
}

// conflictRewriteDenial is the deny for a script writing rel, a file holding conflict
// markers, or for one writing a file it does not name when rel is "".
func conflictRewriteDenial(workspace, rel string) string {
	what, file := "a script rewrites "+rel+", which holds merge conflict markers", rel
	if rel == "" {
		what, file = "a script resolves conflict hunks and writes a file it does not name", "<file>"
	}
	say := what + "; use your editor tool on it."
	// The command names the file, so the verdict does not name it twice.
	if cmd := resolveCommand(workspace, file); cmd != "" && rel != "" {
		say = "a script rewrites a file holding conflict markers; resolve its hunks with `" + cmd + "`."
	} else if cmd != "" {
		say = what + "; resolve its hunks with `" + cmd + "`."
	}
	return say + "\n" +
		"A script that opens a file for writing and then raises leaves it empty, and mid-merge the working copy is the only copy of the half-resolved file.\n" +
		"Your editor tool reads the file first and reports what it changed. Taking one side whole? `git checkout --ours` or `--theirs` names which."
}

// rankConflictRewrite ranks this reason over the rewrite rules' denies, which refuse the
// same line in less specific words, and under every other deny.
func rankConflictRewrite(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" {
		return v
	}
	if v.Deny != "" && !slices.Contains([]denyRuleName{denyRuleInterpreterRewrite, denyRuleInterpreterScript}, v.Rule.Name) {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleConflictRewrite}}
}

// importedPathRe finds the imports a magusfile load error names: `import "./hack/policy/guard"`.
var importedPathRe = regexp.MustCompile(`import "([^"]+)"`)

// conflictedPolicyFiles names the workspace's Buzz sources holding conflict markers when the
// magusfile failed to load, or nil. It reads the root magusfile, each import the error
// names, and that import's sibling .buzz files, since a policy directory imports its own
// files relatively and the error names only the first link of the chain.
func conflictedPolicyFiles(loadFailure error, root string) []string {
	if loadFailure == nil || root == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	check := func(rel string) {
		rel = filepath.Clean(rel)
		if seen[rel] || len(seen) > 64 {
			return
		}
		seen[rel] = true
		if holdsConflictMarkers(filepath.Join(root, rel)) {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	check("magusfile.buzz")
	for _, m := range importedPathRe.FindAllStringSubmatch(loadFailure.Error(), -1) {
		rel := strings.TrimPrefix(m[1], "./")
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		if filepath.Ext(rel) == "" {
			rel += ".buzz"
		}
		check(rel)
		siblings, _ := filepath.Glob(filepath.Join(root, filepath.Dir(rel), "*.buzz"))
		for _, s := range siblings {
			if r, err := filepath.Rel(root, s); err == nil {
				check(r)
			}
		}
	}
	return out
}

// loadFailureLead is the lead ruleFailureNote opens with for the workspace at root whose
// magusfile failed to load with loadFailure.
func loadFailureLead(loadFailure error, root string) string {
	return conflictedPolicyLead(root, conflictedPolicyFiles(loadFailure, root))
}

// conflictedPolicyLead is the line a rule-failure note opens with while conflicted lists
// policy files holding conflict markers, "" when it lists none.
func conflictedPolicyLead(root string, conflicted []string) string {
	if len(conflicted) == 0 {
		return ""
	}
	why := "\nOnly the built-in rules judge calls while these files the magusfile loads hold merge conflict markers: " + strings.Join(conflicted, ", ") + "."
	if cmd := resolveCommand(root, conflicted[0]); cmd != "" {
		return "workspace guard rules are off until this file is resolved: `" + cmd + "`." + why
	}
	return "workspace guard rules are off until " + strings.Join(conflicted, ", ") + " holds no conflict markers." + why
}
